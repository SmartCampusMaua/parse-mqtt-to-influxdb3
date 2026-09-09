package milesight

import (
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

// decodeWS101Entries is shared by every WS101 button variant below — the
// wire protocol and decode logic are identical regardless of button color/
// purpose (the payload never reports that). Only the device_model tag
// differs, since that's a real hardware/deployment distinction worth its
// own queryable InfluxDB tag (e.g. "alert on any SOS press, across every
// SOS unit" without a per-device lookup) — see registry.go's WS101_SOS/
// WS101_SCENE routing entries.
func decodeWS101Entries(entries []TLV, dm, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
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

func decodeWS101(entries []TLV, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeWS101Entries(entries, "ws101", deviceID, provider, ts)
}

func decodeWS101SOS(entries []TLV, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeWS101Entries(entries, "ws101_sos", deviceID, provider, ts)
}

func decodeWS101Scene(entries []TLV, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeWS101Entries(entries, "ws101_scene", deviceID, provider, ts)
}

// DecodeWS101 decodes WS101 binary payloads from LoRaWAN uplinks.
func DecodeWS101(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeWS101(ParseMilesightTLV(payload), deviceID, provider, ts)
}

// DecodeWS101SOS/DecodeWS101Scene decode the SOS (red button) / scene (white
// button) WS101 variants — same wire protocol, see decodeWS101Entries.
func DecodeWS101SOS(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeWS101SOS(ParseMilesightTLV(payload), deviceID, provider, ts)
}

func DecodeWS101Scene(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeWS101Scene(ParseMilesightTLV(payload), deviceID, provider, ts)
}
