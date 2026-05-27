package kron

import (
	"log"
	"math"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

// ks3000IDMap maps measurement IDs (1 byte) to sensor_type strings.
var ks3000IDMap = map[byte]string{
	0x00: "voltage_u_ll_avg",
	0x01: "voltage_u12",
	0x02: "voltage_u23",
	0x03: "voltage_u31",
	0x04: "voltage_u1",
	0x05: "voltage_u2",
	0x06: "voltage_u3",
	0x07: "current_i_avg",
	0x08: "current_in",
	0x09: "current_i1",
	0x0A: "current_i2",
	0x0B: "current_i3",
	0x0C: "frequency",
	0x10: "power_p_total",
	0x11: "power_p1",
	0x12: "power_p2",
	0x13: "power_p3",
	0x14: "power_q_total",
	0x15: "power_q1",
	0x16: "power_q2",
	0x17: "power_q3",
	0x18: "power_s_total",
	0x19: "power_s1",
	0x1A: "power_s2",
	0x1B: "power_s3",
	0x1C: "power_factor",
	0x1D: "power_factor_1",
	0x1E: "power_factor_2",
	0x1F: "power_factor_3",
	0x2E: "energy_a_plus",
	0x2F: "energy_q_plus",
	0x30: "energy_a_minus",
	0x31: "energy_q_minus",
	0x3A: "energy_s_total",
	0x47: "air_temp",
	0x58: "horimetre",
	0xFF: "error_code",
}

func decodeKS3000Float(exp, f2, f1 byte) float64 {
	bits := uint32(exp)<<24 | uint32(f2)<<16 | uint32(f1)<<8
	return float64(math.Float32frombits(bits))
}

func decodeKS3000Timestamp(b []byte) time.Time {
	b3, b4, b5, b6 := b[0], b[1], b[2], b[3]
	sec := int(b3 & 0x3F)
	min := int(b4&0x0F) | int((b3&0xC0)>>2)
	hour := int((b4&0xF0)>>3) | int(b5&0x01)
	day := int((b5 & 0x3E) >> 1)
	mon := int((b5&0xC0)>>4) | int(b6&0x03)
	year := 2000 + int((b6&0xFC)>>2)
	if mon < 1 || mon > 12 || day < 1 || day > 31 {
		log.Printf("[kron] ks3000 timestamp out of range (%d/%d/%d) - using now", day, mon, year)
		return time.Now().UTC()
	}
	return time.Date(year, time.Month(mon), day, hour, min, sec, 0, time.UTC)
}

// DecodeKS3000LORA decodes KS3000 LoRa binary payloads.
func DecodeKS3000LORA(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeKS3000LORA(payload, deviceID, provider, ts)
}

func decodeKS3000LORA(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	if len(payload) < 2 {
		log.Printf("[kron] DecodeKS3000LORA[%s]: payload too short (%d bytes)", deviceID, len(payload))
		return nil
	}
	const dm = "ks3000_lora"
	i := 2
	if i < len(payload) && payload[i] > 0x73 {
		i++
		if i+4 <= len(payload) {
			ts = decodeKS3000Timestamp(payload[i : i+4])
			i += 4
		} else {
			log.Printf("[kron] DecodeKS3000LORA[%s]: truncated timestamp section", deviceID)
		}
	}

	var out []record.SensorDataRecord
	for i+4 <= len(payload) {
		id := payload[i]
		sensorType, ok := ks3000IDMap[id]
		val := decodeKS3000Float(payload[i+1], payload[i+2], payload[i+3])
		if ok {
			out = append(out, record.NewFloat(sensorType, dm, deviceID, provider, val, ts))
		} else {
			log.Printf("[kron] DecodeKS3000LORA[%s]: unknown ID 0x%02X (val=%.4f) - skipped", deviceID, id, val)
		}
		i += 4
	}
	return out
}
