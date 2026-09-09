package imt

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

func TestDecodeLNV3SM3DL_RealPayload(t *testing.T) {
	payload, err := base64.StdEncoding.DecodeString("DQILDQHyDQEgDAzJ")
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}

	records := DecodeLNV3SM3DL(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[int]record.SensorDataRecord{} // DeviceIndex -> record
	var boardVoltage *record.SensorDataRecord
	for i, r := range records {
		if r.SensorType == st.SoilMoistureRaw {
			got[r.DeviceIndex] = r
		}
		if r.SensorType == st.BoardVoltage {
			boardVoltage = &records[i]
		}
	}

	// Raw analog readings — the moisture-% curve is a client-side concern,
	// not baked into the decoder.
	intCases := []struct {
		name        string
		deviceIndex int
		want        int64
	}{
		{"depth 1 (10cm)", 1, 523},
		{"depth 2 (30cm)", 2, 498},
		{"depth 3 (70cm)", 3, 288},
	}
	for _, c := range intCases {
		r, ok := got[c.deviceIndex]
		if !ok || r.ValueInt == nil {
			t.Fatalf("%s: no int record, got %+v", c.name, r)
		}
		if *r.ValueInt != c.want {
			t.Errorf("%s: got %d, want %d", c.name, *r.ValueInt, c.want)
		}
	}

	if boardVoltage == nil || boardVoltage.ValueFloat == nil {
		t.Fatalf("BoardVoltage: no float record")
	}
	bv := *boardVoltage
	if want := 3.273; *bv.ValueFloat != want {
		t.Errorf("BoardVoltage: got %v, want %v", *bv.ValueFloat, want)
	}
}
