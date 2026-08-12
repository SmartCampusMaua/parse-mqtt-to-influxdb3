package milesight

import (
	"log"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

func decodeEM500SMTC(entries []TLV, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "em500_smtc"
	st := record.ST
	var out []record.SensorDataRecord
	for _, e := range entries {
		switch {
		case e.Channel == 0x01 && e.Type == 0x75:
			out = append(out, record.NewFloat(st.BatteryLevel, dm, deviceID, provider, float64(e.Data[0]), ts))

		case e.Channel == 0x03 && e.Type == 0x67:
			raw := le16(e.Data)
			switch raw {
			case 0xFFFF:
				log.Printf("[milesight] EM500-SMTC[%s]: temperature collection failed", deviceID)
			case 0xFFFD:
				log.Printf("[milesight] EM500-SMTC[%s]: temperature out of range", deviceID)
			default:
				out = append(out, record.NewFloat(st.SoilTemp, dm, deviceID, provider, float64(int16(raw))/10.0, ts))
			}

		// moisture, old resolution (0.5%)
		case e.Channel == 0x04 && e.Type == 0x68:
			out = append(out, record.NewFloat(st.SoilMoisture, dm, deviceID, provider, float64(e.Data[0])/2.0, ts))

		// moisture, new resolution (0.01%)
		case e.Channel == 0x04 && e.Type == 0xCA:
			raw := le16(e.Data)
			switch raw {
			case 0xFFFF:
				log.Printf("[milesight] EM500-SMTC[%s]: moisture collection failed", deviceID)
			case 0xFFFD:
				log.Printf("[milesight] EM500-SMTC[%s]: moisture out of range", deviceID)
			default:
				out = append(out, record.NewFloat(st.SoilMoisture, dm, deviceID, provider, float64(raw)/100.0, ts))
			}

		case e.Channel == 0x05 && e.Type == 0x7F:
			raw := le16(e.Data)
			switch raw {
			case 0xFFFF:
				log.Printf("[milesight] EM500-SMTC[%s]: electrical conductivity collection failed", deviceID)
			case 0xFFFD:
				log.Printf("[milesight] EM500-SMTC[%s]: electrical conductivity out of range", deviceID)
			default:
				out = append(out, record.NewInt(st.ElectricalConductivity, dm, deviceID, provider, int64(raw), ts))
			}

		// temperature + mutation alarm channel: temp(2B)+mutation(2B)+alarm_type(1B)
		case e.Channel == 0x83 && e.Type == 0xD7:
			out = append(out, record.NewFloat(st.SoilTemp, dm, deviceID, provider, float64(int16(le16(e.Data[0:2])))/10.0, ts))
			out = append(out, record.NewFloat(st.TemperatureMutation, dm, deviceID, provider, float64(int16(le16(e.Data[2:4])))/10.0, ts))
			out = append(out, record.NewInt(st.TemperatureAlarm, dm, deviceID, provider, int64(e.Data[4]), ts))
		}
	}
	return out
}

// DecodeEM500SMTC decodes EM500-SMTC binary payloads from LoRaWAN uplinks.
func DecodeEM500SMTC(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeEM500SMTC(ParseMilesightTLV(payload), deviceID, provider, ts)
}
