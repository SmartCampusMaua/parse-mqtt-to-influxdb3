package milesight

import (
	"log"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/internal/record"
)

func decodeEM500SWL(entries []TLV, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "em500_swl"
	st := record.ST
	var out []record.SensorDataRecord
	for _, e := range entries {
		switch {
		case e.Channel == 0x01 && e.Type == 0x75:
			out = append(out, record.NewFloat(st.BatteryLevel, dm, deviceID, provider, float64(e.Data[0]), ts))
		case e.Channel == 0x03 && e.Type == 0x77:
			raw := le16(e.Data)
			switch raw {
			case 0xFFFF:
				log.Printf("[milesight] EM500-SWL[%s]: depth sensor collection failed", deviceID)
			case 0xFFFD:
				log.Printf("[milesight] EM500-SWL[%s]: depth sensor out of range", deviceID)
			default:
				out = append(out, record.NewFloat(st.WaterLevel, dm, deviceID, provider, float64(raw)/100.0, ts))
			}
		}
	}
	return out
}

// DecodeEM500SWL decodes EM500-SWL binary payloads from LoRaWAN uplinks.
func DecodeEM500SWL(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeEM500SWL(ParseMilesightTLV(payload), deviceID, provider, ts)
}
