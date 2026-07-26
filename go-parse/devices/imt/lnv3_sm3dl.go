package imt

import (
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

// decodeLNV3SM3DL writes the raw analog probe readings (tag 0x0D) as
// smdl1/2/3. The moisture-percentage curve (15000 × raw^power, scaled per
// depth) is a per-probe cloud calibration, not a device measurement — it
// belongs in each device's sensor_calibration entry (scale/offset/power in
// devices.json), applied downstream, not hardcoded here.
func decodeLNV3SM3DL(data IMT, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "lnv3_sm3dl"
	st := record.ST
	var out []record.SensorDataRecord

	out = append(out, record.NewInt(st.SMDL1, dm, deviceID, provider, int64(data.X_0D_0), ts))
	out = append(out, record.NewInt(st.SMDL2, dm, deviceID, provider, int64(data.X_0D_1), ts))
	out = append(out, record.NewInt(st.SMDL3, dm, deviceID, provider, int64(data.X_0D_2), ts))
	out = append(out, record.NewFloat(st.BoardVoltage, dm, deviceID, provider, data.X_0C, ts))

	return out
}

// DecodeLNV3SM3DL decodes IMT LoraNodeV3-SM3DL (Soil Moisture 3 Depth Levels)
// binary payloads from LoRaWAN uplinks.
func DecodeLNV3SM3DL(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeLNV3SM3DL(parseIMT(payload), deviceID, provider, ts)
}
