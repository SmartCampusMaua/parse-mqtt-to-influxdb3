package milesight

import (
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

func decodeWS101(entries []TLV, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "ws101"
	st := record.ST
	var out []record.SensorDataRecord
	for _, e := range entries {
		switch {
		case e.Channel == 0x01 && e.Type == 0x75:
			out = append(out, record.NewFloat(st.BatteryLevel, dm, deviceID, provider, float64(e.Data[0]), ts))
		case e.Channel == 0xFF && e.Type == 0x2E:
			out = append(out, record.NewInt(st.PressType, dm, deviceID, provider, int64(e.Data[0]), ts))
		}
	}
	return out
}

// DecodeWS101 decodes WS101 binary payloads from LoRaWAN uplinks.
func DecodeWS101(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeWS101(ParseMilesightTLV(payload), deviceID, provider, ts)
}
