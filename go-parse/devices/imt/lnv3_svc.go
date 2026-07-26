package imt

import (
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

// solenoidOpenThreshold is the raw analog reading (tag 0x0D) above which a
// solenoid valve channel is considered open/energized.
const solenoidOpenThreshold = 1500

func decodeLNV3SVC(data IMT, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "lnv3_svc"
	st := record.ST
	var out []record.SensorDataRecord

	out = append(out, record.NewBool(st.SV1, dm, deviceID, provider, data.X_0D_0 > solenoidOpenThreshold, ts))
	out = append(out, record.NewBool(st.SV2, dm, deviceID, provider, data.X_0D_1 > solenoidOpenThreshold, ts))
	out = append(out, record.NewBool(st.SV3, dm, deviceID, provider, data.X_0D_2 > solenoidOpenThreshold, ts))
	out = append(out, record.NewInt(st.PulseCount, dm, deviceID, provider, int64(data.X_0B), ts))
	out = append(out, record.NewFloat(st.BoardVoltage, dm, deviceID, provider, data.X_0C, ts))

	return out
}

// DecodeLNV3SVC decodes IMT LoraNodeV3-SVC binary payloads from LoRaWAN uplinks.
func DecodeLNV3SVC(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeLNV3SVC(parseIMT(payload), deviceID, provider, ts)
}
