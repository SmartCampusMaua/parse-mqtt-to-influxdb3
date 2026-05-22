package milesight

import (
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/internal/record"
)

func decodeEM300DI(entries []TLV, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "em300_di"
	st := record.ST
	var out []record.SensorDataRecord
	for _, e := range entries {
		switch {
		case e.Channel == 0x01 && e.Type == 0x75:
			out = append(out, record.NewFloat(st.BatteryLevel, dm, deviceID, provider, float64(e.Data[0]), ts))
		case e.Channel == 0x03 && e.Type == 0x67:
			out = append(out, record.NewFloat(st.AirTemp, dm, deviceID, provider, float64(int16(le16(e.Data)))/10.0, ts))
		case e.Channel == 0x04 && e.Type == 0x68:
			out = append(out, record.NewFloat(st.AirRH, dm, deviceID, provider, float64(e.Data[0])/2.0, ts))
		case e.Channel == 0x05 && e.Type == 0x00:
			out = append(out, record.NewBool(st.PulseState, dm, deviceID, provider, e.Data[0] == 0x01, ts))
		case e.Channel == 0x05 && e.Type == 0xC8:
			out = append(out, record.NewInt(st.PulseCounter, dm, deviceID, provider, int64(le32(e.Data)), ts))
		}
	}
	return out
}

// DecodeEM300DI decodes EM300-DI binary payloads from LoRaWAN uplinks.
func DecodeEM300DI(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeEM300DI(ParseMilesightTLV(payload), deviceID, provider, ts)
}
