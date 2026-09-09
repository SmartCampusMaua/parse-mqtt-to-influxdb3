package imt

import (
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

// decodeLNV3SM3DL writes the raw analog probe readings (tag 0x0D) as
// soil_moisture_raw, DeviceIndex 1/2/3 (depth 10/30/70 cm). The moisture-
// percentage curve (15000 × raw^power, scaled per depth) is a client-side/
// user-space concern applied by whatever consumes the SmartCampusMaua API —
// this pipeline only ever writes the raw reading it received, untouched.
func decodeLNV3SM3DL(data IMT, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "lnv3_sm3dl"
	st := record.ST
	var out []record.SensorDataRecord

	for i, raw := range []uint64{data.X_0D_0, data.X_0D_1, data.X_0D_2} {
		r := record.NewInt(st.SoilMoistureRaw, dm, deviceID, provider, int64(raw), ts)
		r.DeviceIndex = i + 1
		out = append(out, r)
	}
	out = append(out, record.NewFloat(st.BoardVoltage, dm, deviceID, provider, data.X_0C, ts))

	return out
}

// DecodeLNV3SM3DL decodes IMT LoraNodeV3-SM3DL (Soil Moisture 3 Depth Levels)
// binary payloads from LoRaWAN uplinks.
func DecodeLNV3SM3DL(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeLNV3SM3DL(parseIMT(payload), deviceID, provider, ts)
}
