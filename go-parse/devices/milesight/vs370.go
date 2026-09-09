// Reference: https://github.com/Milesight-IoT/SensorDecoders/blob/main/vs-series/vs370/vs370-decoder.js
//
// VS370 is a simple single-zone occupancy/illuminance presence sensor —
// despite the similar name, it is not a smaller VS373: VS373 is a
// multi-region bed/room presence + vital-signs sensor with per-region
// booleans; VS370 has exactly one occupancy reading and one illuminance
// reading, both enums, no regions. Uses the standard static-length
// ParseMilesightTLV — no RS485 port, nothing site-configurable.
package milesight

import (
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

const dmVS370 = "vs370"

// decodeVS370 decodes VS370 fixed-purpose channels. Downlink command echoes
// (reboot ack, report_status, D2D key/enable, sync_time, bluetooth_enable,
// etc. under channels 0xFE/0xFF/0xF8/0xF9) are not decoded into sensor_data
// records — same precedent as every other decoder's downlink-response
// handling in this package.
func decodeVS370(entries []TLV, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = dmVS370
	st := record.ST
	var out []record.SensorDataRecord

	for _, e := range entries {
		switch {
		case e.Channel == 0x01 && e.Type == 0x75:
			out = append(out, record.NewFloat(st.BatteryLevel, dm, deviceID, provider, float64(e.Data[0]), ts))
		case e.Channel == 0x03 && e.Type == 0x00:
			out = append(out, record.NewBool(st.OccupancyStatus, dm, deviceID, provider, e.Data[0] == 1, ts))
		case e.Channel == 0x04 && e.Type == 0x00:
			out = append(out, record.NewInt(st.IlluminanceStatus, dm, deviceID, provider, int64(e.Data[0]), ts))
		}
	}

	return out
}

// DecodeVS370 decodes VS370 binary payloads from LoRaWAN uplinks.
func DecodeVS370(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeVS370(ParseMilesightTLV(payload), deviceID, provider, ts)
}
