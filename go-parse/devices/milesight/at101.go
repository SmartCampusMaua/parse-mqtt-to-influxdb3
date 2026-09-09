package milesight

import (
	"log"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

func decodeAT101(entries []TLV, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "at101"
	st := record.ST
	var out []record.SensorDataRecord
	for _, e := range entries {
		switch {
		case e.Channel == 0x01 && e.Type == 0x75:
			out = append(out, record.NewFloat(st.BatteryLevel, dm, deviceID, provider, float64(e.Data[0]), ts))

		case e.Channel == 0x03 && e.Type == 0x67:
			out = append(out, record.NewFloat(st.AirTemp, dm, deviceID, provider, float64(int16(le16(e.Data)))/10.0, ts))

		// temperature + abnormal alarm: temp(2B)+alarm(1B, 0=normal 1=abnormal)
		case e.Channel == 0x83 && e.Type == 0x67:
			out = append(out, record.NewFloat(st.AirTemp, dm, deviceID, provider, float64(int16(le16(e.Data[0:2])))/10.0, ts))
			out = append(out, record.NewInt(st.AirTempAlarm, dm, deviceID, provider, int64(e.Data[2]), ts))

		// location (0x04 normal report, 0x84 geofence/alarm report): lat(4B)+lon(4B)+status(1B)
		// status low nibble = motion_status, high nibble = geofence_status
		case (e.Channel == 0x04 || e.Channel == 0x84) && e.Type == 0x88:
			latRaw := int32(le32(e.Data[0:4]))
			lonRaw := int32(le32(e.Data[4:8]))
			// The device sends raw -1 (0xFFFFFFFF) for lat/lon when it hasn't
			// obtained a GPS/WiFi fix yet. The official Milesight decoder
			// doesn't filter this and divides it through anyway, producing a
			// bogus (-0.000001, -0.000001) point near Null Island — skip it
			// here instead of writing a fake location to sensor_data.
			if latRaw == -1 || lonRaw == -1 {
				log.Printf("[milesight] AT101[%s]: no GPS/WiFi fix yet, skipping location", deviceID)
			} else {
				out = append(out, record.NewFloat(st.Latitude, dm, deviceID, provider, float64(latRaw)/1000000.0, ts))
				out = append(out, record.NewFloat(st.Longitude, dm, deviceID, provider, float64(lonRaw)/1000000.0, ts))
			}
			status := e.Data[8]
			out = append(out, record.NewInt(st.MotionStatus, dm, deviceID, provider, int64(status&0x0F), ts))
			out = append(out, record.NewInt(st.GeofenceStatus, dm, deviceID, provider, int64(status>>4), ts))

		case e.Channel == 0x05 && e.Type == 0x00:
			out = append(out, record.NewInt(st.DevicePosition, dm, deviceID, provider, int64(e.Data[0]), ts))

		case e.Channel == 0x07 && e.Type == 0x00:
			out = append(out, record.NewInt(st.TamperStatus, dm, deviceID, provider, int64(e.Data[0]), ts))

			// 0x06/0xD9 (wifi scan result) is tokenized via channelTypeOverride but
			// intentionally not decoded: it carries a MAC address and can repeat
			// multiple times per uplink, which doesn't fit the one-value-per-
			// sensor_type-per-timestamp model.
		}
	}
	return out
}

// DecodeAT101 decodes AT101 binary payloads from LoRaWAN uplinks.
func DecodeAT101(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeAT101(ParseMilesightTLV(payload), deviceID, provider, ts)
}
