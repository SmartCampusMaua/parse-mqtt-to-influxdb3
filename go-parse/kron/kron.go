// Package kron decodes Kron KS3000 power-meter payloads.
//
//   KS3000_LORA – LoRaWAN binary (22 bytes = 11 × int16 big-endian)
//   KS3000_WIFI – Direct MQTT JSON array [{"variable":"data","time":"…","metadata":{…}}]
//
// Both variants produce identical sensor_type values (record.ST) so queries
// can target all KS3000 devices with  WHERE device_model LIKE 'ks3000%'.
package kron

import (
	"encoding/binary"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/record"
)

// Metadata mirrors the "metadata" object in a KS3000-WiFi MQTT message.
type Metadata struct {
	U0  float64 `json:"U0"`  // voltage_u_ll_avg
	I0  float64 `json:"I0"`  // current_i_avg
	F1  float64 `json:"F1"`  // frequency
	P0  float64 `json:"P0"`  // power_p_total
	Q0  float64 `json:"Q0"`  // power_q_total
	FP0 float64 `json:"FP0"` // power_factor
	EA  float64 `json:"EA"`  // energy_a_plus
	ER  float64 `json:"ER"`  // energy_q_plus
	EAN float64 `json:"EAN"` // energy_a_minus
	ERN float64 `json:"ERN"` // energy_q_minus
	CE  float64 `json:"CE"`  // error_code
}

// Message is one element of the KS3000-WiFi JSON array.
type Message struct {
	Variable string   `json:"variable"`
	Time     string   `json:"time"` // "2006-01-02 15:04:05"
	Metadata Metadata `json:"metadata"`
}

// buildRecords maps a Metadata struct to []SensorDataRecord using record.ST names.
// error_code is always written; 0 = "no error" is a meaningful state.
func buildRecords(md Metadata, dm, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	st := record.ST
	var out []record.SensorDataRecord
	if md.U0  != 0 { out = append(out, record.NewFloat(st.VoltageULLAvg, dm, deviceID, provider, md.U0,  ts)) }
	if md.I0  != 0 { out = append(out, record.NewFloat(st.CurrentIAvg,   dm, deviceID, provider, md.I0,  ts)) }
	if md.F1  != 0 { out = append(out, record.NewFloat(st.Frequency,     dm, deviceID, provider, md.F1,  ts)) }
	if md.P0  != 0 { out = append(out, record.NewFloat(st.PowerPTotal,   dm, deviceID, provider, md.P0,  ts)) }
	if md.Q0  != 0 { out = append(out, record.NewFloat(st.PowerQTotal,   dm, deviceID, provider, md.Q0,  ts)) }
	if md.FP0 != 0 { out = append(out, record.NewFloat(st.PowerFactor,   dm, deviceID, provider, md.FP0, ts)) }
	if md.EA  != 0 { out = append(out, record.NewFloat(st.EnergyAPlus,   dm, deviceID, provider, md.EA,  ts)) }
	if md.ER  != 0 { out = append(out, record.NewFloat(st.EnergyQPlus,   dm, deviceID, provider, md.ER,  ts)) }
	if md.EAN != 0 { out = append(out, record.NewFloat(st.EnergyAMinus,  dm, deviceID, provider, md.EAN, ts)) }
	if md.ERN != 0 { out = append(out, record.NewFloat(st.EnergyQMinus,  dm, deviceID, provider, md.ERN, ts)) }
	out = append(out, record.NewFloat(st.ErrorCode, dm, deviceID, provider, md.CE, ts))
	return out
}

// DecodeLoRa decodes a KS3000-LoRa binary payload (22 bytes, 11 × int16 BE).
// Channels 0-5 (instantaneous) ÷100; channels 6-9 (energy) ÷1 (integer kWh).
func DecodeLoRa(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "ks3000_lora"
	if len(payload) < 22 {
		log.Printf("[kron] DecodeLoRa[%s]: short payload (%d bytes, need 22)", deviceID, len(payload))
		return nil
	}
	ch := func(i int) float64 {
		return float64(int16(binary.BigEndian.Uint16(payload[i*2 : i*2+2])))
	}
	md := Metadata{
		U0: ch(0)/100, I0: ch(1)/100, F1: ch(2)/100,
		P0: ch(3)/100, Q0: ch(4)/100, FP0: ch(5)/100,
		EA: ch(6), ER: ch(7), EAN: ch(8), ERN: ch(9), CE: ch(10),
	}
	return buildRecords(md, dm, deviceID, provider, ts)
}

// DecodeWiFi decodes a KS3000-WiFi direct-MQTT JSON array payload.
// Timestamp is extracted from the message "time" field.
// deviceModel should be the lowercase string (e.g. "ks3000_wifi").
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
