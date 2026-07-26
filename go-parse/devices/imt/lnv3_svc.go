package imt

import (
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

// decodeLNV3SVC writes the raw analog probe readings (tag 0x0D) as sv1/2/3.
// The open/closed decision (raw above some threshold) is a per-device cloud
// calibration, not a device measurement — it belongs in each device's
// sensor_calibration entry (offset in devices.json), applied downstream as
// calibrated = raw + offset, open when calibrated > 0.
func decodeLNV3SVC(data IMT, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "lnv3_svc"
	st := record.ST
	var out []record.SensorDataRecord

	out = append(out, record.NewInt(st.SV1, dm, deviceID, provider, int64(data.X_0D_0), ts))
	out = append(out, record.NewInt(st.SV2, dm, deviceID, provider, int64(data.X_0D_1), ts))
	out = append(out, record.NewInt(st.SV3, dm, deviceID, provider, int64(data.X_0D_2), ts))
	out = append(out, record.NewInt(st.PulseCount, dm, deviceID, provider, int64(data.X_0B), ts))
	out = append(out, record.NewFloat(st.BoardVoltage, dm, deviceID, provider, data.X_0C, ts))

	return out
}

// DecodeLNV3SVC decodes IMT LoraNodeV3-SVC binary payloads from LoRaWAN uplinks.
func DecodeLNV3SVC(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeLNV3SVC(parseIMT(payload), deviceID, provider, ts)
}
