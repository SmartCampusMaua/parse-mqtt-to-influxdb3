// Package kron decodes Kron KS3000 power-meter payloads.
//
// Two connectivity variants share identical sensor_type names:
//
//	KS3000_WIFI  direct MQTT  JSON array  [{"variable":"data","time":"…","metadata":{…}}]
//	KS3000_LORA  LoRaWAN      binary      Kron IoT protocol (see section 6 of the manual)
//
// Query both together: WHERE device_model LIKE 'ks3000%'
package kron

import (
	"encoding/binary"
	"encoding/json"
	"log"
	"math"
	"strings"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/record"
)

// ─── KS3000-WiFi (direct MQTT JSON) ──────────────────────────────────────────

type Metadata struct {
	U0  float64 `json:"U0"`
	I0  float64 `json:"I0"`
	F1  float64 `json:"F1"`
	P0  float64 `json:"P0"`
	Q0  float64 `json:"Q0"`
	FP0 float64 `json:"FP0"`
	EA  float64 `json:"EA"`
	ER  float64 `json:"ER"`
	EAN float64 `json:"EAN"`
	ERN float64 `json:"ERN"`
	CE  float64 `json:"CE"`
}

type Message struct {
	Variable string   `json:"variable"`
	Time     string   `json:"time"`
	Metadata Metadata `json:"metadata"`
}

func buildRecords(md Metadata, dm, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	st := record.ST
	var out []record.SensorDataRecord
	if md.U0 != 0 {
		out = append(out, record.NewFloat(st.VoltageULLAvg, dm, deviceID, provider, md.U0, ts))
	}
	if md.I0 != 0 {
		out = append(out, record.NewFloat(st.CurrentIAvg, dm, deviceID, provider, md.I0, ts))
	}
	if md.F1 != 0 {
		out = append(out, record.NewFloat(st.Frequency, dm, deviceID, provider, md.F1, ts))
	}
	if md.P0 != 0 {
		out = append(out, record.NewFloat(st.PowerPTotal, dm, deviceID, provider, md.P0, ts))
	}
	if md.Q0 != 0 {
		out = append(out, record.NewFloat(st.PowerQTotal, dm, deviceID, provider, md.Q0, ts))
	}
	if md.FP0 != 0 {
		out = append(out, record.NewFloat(st.PowerFactor, dm, deviceID, provider, md.FP0, ts))
	}
	if md.EA != 0 {
		out = append(out, record.NewFloat(st.EnergyAPlus, dm, deviceID, provider, md.EA, ts))
	}
	if md.ER != 0 {
		out = append(out, record.NewFloat(st.EnergyQPlus, dm, deviceID, provider, md.ER, ts))
	}
	if md.EAN != 0 {
		out = append(out, record.NewFloat(st.EnergyAMinus, dm, deviceID, provider, md.EAN, ts))
	}
	if md.ERN != 0 {
		out = append(out, record.NewFloat(st.EnergyQMinus, dm, deviceID, provider, md.ERN, ts))
	}
	out = append(out, record.NewFloat(st.ErrorCode, dm, deviceID, provider, md.CE, ts))
	return out
}

func DecodeWiFi(message, deviceID, deviceModel string) []record.SensorDataRecord {
	var msgs []Message
	if err := json.Unmarshal([]byte(message), &msgs); err != nil || len(msgs) == 0 {
		log.Printf("[kron] DecodeWiFi[%s]: %v", deviceID, err)
		return nil
	}
	entry := msgs[0]
	t, err := time.Parse("2006-01-02 15:04:05", entry.Time)
	if err != nil {
		log.Printf("[kron] DecodeWiFi[%s]: timestamp error (%v), using now", deviceID, err)
		t = time.Now().UTC()
	}
	return buildRecords(entry.Metadata, strings.ToLower(deviceModel), deviceID, "custom", t)
}

// ─── KS3000-LoRa (LoRaWAN binary) ────────────────────────────────────────────
//
// Protocol reference: Kron KS3000 / Konect LoRa uplink specification section 6.
//
// Payload layout:
//   bytes[0]     instrument code  (0xF2=KS3000, 0xB0=Konect, 0xF3=Konect 120, etc.)
//   bytes[1]     firmware version (encoded as version×10, e.g. 0x30 = v4.8)
//
//   IF bytes[2] > 0x73:          ← special-code byte present
//     bytes[2]   special code    (0xFF = "none")
//     bytes[3..6] packed 4-byte timestamp (see decodeKS3000Timestamp)
//     measurements start at bytes[7]
//   ELSE:                         ← no special code / timestamp
//     measurements start at bytes[2]
//
//   Each measurement = 4 bytes: [id(1B)][EXP(1B)][F2(1B)][F1(1B)]
//   Exception: ID 0xFF followed by 0xCE = error code (5 bytes total)
//
// Float encoding (EXP, F2, F1):
//   These are the 3 most-significant bytes of an IEEE 754 float32 big-endian.
//   The 4th byte (LSB of mantissa) is implicitly 0.
//   Decode: float32frombits( uint32(EXP)<<24 | uint32(F2)<<16 | uint32(F1)<<8 )
//
// Timestamp packing (4 bytes, decimal values, not BCD):
//   Seconds = b3 & 0x3F
//   Minutes = (b4 & 0x0F) | ((b3 & 0xC0) >> 2)
//   Hour    = ((b4 & 0xF0) >> 3) | (b5 & 0x01)
//   Day     = (b5 & 0x3E) >> 1
//   Month   = ((b5 & 0xC0) >> 4) | (b6 & 0x03)
//   Year    = 2000 + ((b6 & 0xFC) >> 2)

// ks3000IDMap maps measurement IDs (1 byte) to sensor_type strings.
// IDs 0x00–0x73 are standard measurements; others are reserved or special.
// Unmapped IDs are logged and skipped.
var ks3000IDMap = map[byte]string{
	// ── Voltages ─────────────────────────────────────────────────────────
	0x00: "voltage_u_ll_avg", // U0  three-phase average (V)
	0x01: "voltage_u12",      // U12 line L1-L2 (V)
	0x02: "voltage_u23",      // U23 line L2-L3 (V)
	0x03: "voltage_u31",      // U31 line L3-L1 (V)
	0x04: "voltage_u1",       // U1  phase 1 (V)
	0x05: "voltage_u2",       // U2  phase 2 (V)
	0x06: "voltage_u3",       // U3  phase 3 (V)
	// ── Currents ─────────────────────────────────────────────────────────
	0x07: "current_i_avg", // I0  three-phase (A)
	0x08: "current_in",    // IN  neutral (A)
	0x09: "current_i1",    // I1  phase 1 (A)
	0x0A: "current_i2",    // I2  phase 2 (A)
	0x0B: "current_i3",    // I3  phase 3 (A)
	// ── Frequency ────────────────────────────────────────────────────────
	0x0C: "frequency", // F1  Hz
	// ── Active power ─────────────────────────────────────────────────────
	0x10: "power_p_total", // P0  three-phase (W)
	0x11: "power_p1",      // P1  (W)
	0x12: "power_p2",
	0x13: "power_p3",
	// ── Reactive power ───────────────────────────────────────────────────
	0x14: "power_q_total", // Q0  three-phase (VAr)
	0x15: "power_q1",
	0x16: "power_q2",
	0x17: "power_q3",
	// ── Apparent power ───────────────────────────────────────────────────
	0x18: "power_s_total", // S0  three-phase (VA)
	0x19: "power_s1",
	0x1A: "power_s2",
	0x1B: "power_s3",
	// ── Power factor ─────────────────────────────────────────────────────
	0x1C: "power_factor", // FP0 three-phase
	0x1D: "power_factor_1",
	0x1E: "power_factor_2",
	0x1F: "power_factor_3",
	// ── Energy ───────────────────────────────────────────────────────────
	0x2E: "energy_a_plus",  // EA  import active (kWh)
	0x2F: "energy_q_plus",  // ER  import reactive (kVArh)
	0x30: "energy_a_minus", // EAN export active (kWh)
	0x31: "energy_q_minus", // ERN export reactive (kVArh)
	0x3A: "energy_s_total", // ES  apparent (kVAh)
	// ── Misc ─────────────────────────────────────────────────────────────
	0x47: "air_temp",   // TEMP °C
	0x58: "horimetre",  // HORIM hour meter (h)
	0xFF: "error_code", // CE   error code (0=ok)
}

// decodeKS3000Float converts 3 bytes (EXP, F2, F1) to float64.
// The KS3000 stores values as the 3 most-significant bytes of IEEE 754 float32 BE.
func decodeKS3000Float(exp, f2, f1 byte) float64 {
	bits := uint32(exp)<<24 | uint32(f2)<<16 | uint32(f1)<<8
	return float64(math.Float32frombits(bits))
}

// decodeKS3000Timestamp unpacks the 4-byte compact timestamp.
// Year field is an offset from 2000 (6 bits → range 2000–2063).
func decodeKS3000Timestamp(b []byte) time.Time {
	b3, b4, b5, b6 := b[0], b[1], b[2], b[3]
	sec := int(b3 & 0x3F)
	min := int(b4&0x0F) | int((b3&0xC0)>>2)
	hour := int((b4&0xF0)>>3) | int(b5&0x01)
	day := int((b5 & 0x3E) >> 1)
	mon := int((b5&0xC0)>>4) | int(b6&0x03)
	year := 2000 + int((b6&0xFC)>>2)
	if mon < 1 || mon > 12 || day < 1 || day > 31 {
		log.Printf("[kron] ks3000 timestamp out of range (%d/%d/%d) – using now", day, mon, year)
		return time.Now().UTC()
	}
	return time.Date(year, time.Month(mon), day, hour, min, sec, 0, time.UTC)
}

// DecodeKS300LORA decodes a KS3000-LoRa binary payload (Kron IoT specification).
func DecodeKS300LORA(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	if len(payload) < 2 {
		log.Printf("[kron] DecodeKS300_LORA[%s]: payload too short (%d bytes)", deviceID, len(payload))
		return nil
	}
	const dm = "ks3000_lora"
	_ = binary.BigEndian // imported for clarity

	// ── Parse header ───────────────────────────────────────────────────────
	// bytes[0] = instrument code, bytes[1] = firmware version (×10)
	i := 2

	// ── Detect optional special-code + timestamp section ──────────────────
	// If bytes[2] > 0x73 it is a special-code byte (0xFF = "no special code"),
	// followed by a 4-byte packed timestamp.  Measurements start at bytes[7].
	// If bytes[2] ≤ 0x73 it is already the first measurement ID.
	if i < len(payload) && payload[i] > 0x73 {
		i++ // skip special-code byte
		if i+4 <= len(payload) {
			ts = decodeKS3000Timestamp(payload[i : i+4])
			i += 4
		} else {
			log.Printf("[kron] DecodeLoRa[%s]: truncated timestamp section", deviceID)
		}
	}

	// ── Parse repeating 4-byte measurement groups ──────────────────────────
	var out []record.SensorDataRecord
	for i+4 <= len(payload) {
		id := payload[i]

		// Each measurement = 4 bytes: [id][EXP][F2][F1]
		// ID 0xFF = error_code (4 bytes, CE is the abbreviation not a sub-byte)
		sensorType, ok := ks3000IDMap[id]
		val := decodeKS3000Float(payload[i+1], payload[i+2], payload[i+3])
		if ok {
			out = append(out, record.NewFloat(sensorType, dm, deviceID, provider, val, ts))
		} else {
			log.Printf("[kron] DecodeLoRa[%s]: unknown ID 0x%02X (val=%.4f) – skipped", deviceID, id, val)
		}
		i += 4
	}
	return out
}
