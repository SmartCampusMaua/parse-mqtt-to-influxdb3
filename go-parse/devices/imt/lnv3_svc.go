package imt

import (
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

// decodeLNV3SVC writes the raw analog probe readings (tag 0x0D) as
// solenoid_valve_raw, DeviceIndex 1/2/3. The open/closed decision (raw above
// some threshold) is a client-side/user-space concern applied by whatever
// consumes the SmartCampusMaua API — this pipeline only ever writes the raw
// reading it received, untouched.
func decodeLNV3SVC(data IMT, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "lnv3_svc"
	st := record.ST
	var out []record.SensorDataRecord

	for i, raw := range []uint64{data.X_0D_0, data.X_0D_1, data.X_0D_2} {
		r := record.NewInt(st.SolenoidValveRaw, dm, deviceID, provider, int64(raw), ts)
		r.DeviceIndex = i + 1
		out = append(out, r)
	}
	out = append(out, record.NewInt(st.PulseCount, dm, deviceID, provider, int64(data.X_0B), ts))
	out = append(out, record.NewFloat(st.BoardVoltage, dm, deviceID, provider, data.X_0C, ts))

	return out
}

// DecodeLNV3SVC decodes IMT LoraNodeV3-SVC binary payloads from LoRaWAN uplinks.
func DecodeLNV3SVC(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeLNV3SVC(parseIMT(payload), deviceID, provider, ts)
}
