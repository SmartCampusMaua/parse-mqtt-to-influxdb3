package kron

import (
	"log"
	"math"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

// ks3000IDMap maps measurement IDs (1 byte) to sensor_type strings.
var ks3000IDMap = map[byte]string{
	0x00: record.ST.VoltageULLAvg,
	0x01: record.ST.VoltageU12,
	0x02: record.ST.VoltageU23,
	0x03: record.ST.VoltageU31,
	0x04: record.ST.VoltageU1,
	0x05: record.ST.VoltageU2,
	0x06: record.ST.VoltageU3,
	0x07: record.ST.CurrentIAvg,
	0x08: record.ST.CurrentIN,
	0x09: record.ST.CurrentI1,
	0x0A: record.ST.CurrentI2,
	0x0B: record.ST.CurrentI3,
	0x0C: record.ST.Frequency,
	0x10: record.ST.PowerPTotal,
	0x11: record.ST.PowerP1,
	0x12: record.ST.PowerP2,
	0x13: record.ST.PowerP3,
	0x14: record.ST.PowerQTotal,
	0x15: record.ST.PowerQ1,
	0x16: record.ST.PowerQ2,
	0x17: record.ST.PowerQ3,
	0x18: record.ST.PowerSTotal,
	0x19: record.ST.PowerS1,
	0x1A: record.ST.PowerS2,
	0x1B: record.ST.PowerS3,
	0x1C: record.ST.PowerFactor,
	0x1D: record.ST.PowerFactor1,
	0x1E: record.ST.PowerFactor2,
	0x1F: record.ST.PowerFactor3,
	0x2E: record.ST.EnergyAPlus,
	0x2F: record.ST.EnergyQPlus,
	0x30: record.ST.EnergyAMinus,
	0x31: record.ST.EnergyQMinus,
	0x3A: record.ST.EnergySTotal,
	0x47: record.ST.AirTemp,
	0x58: record.ST.Horimetre,
	0xFF: record.ST.ErrorCode,
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
