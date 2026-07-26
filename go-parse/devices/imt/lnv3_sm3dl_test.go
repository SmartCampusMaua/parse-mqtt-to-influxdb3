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
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	// Raw analog readings — the moisture-% curve is applied downstream via
	// sensor_calibration, not baked into the decoder.
	intCases := []struct {
		name string
		st   string
		want int64
	}{
		{"SMDL1", st.SMDL1, 523},
		{"SMDL2", st.SMDL2, 498},
		{"SMDL3", st.SMDL3, 288},
	}
	for _, c := range intCases {
		r, ok := got[c.st]
		if !ok || r.ValueInt == nil {
			t.Fatalf("%s: no int record, got %+v", c.name, r)
		}
		if *r.ValueInt != c.want {
			t.Errorf("%s: got %d, want %d", c.name, *r.ValueInt, c.want)
		}
	}

	bv, ok := got[st.BoardVoltage]
	if !ok || bv.ValueFloat == nil {
		t.Fatalf("BoardVoltage: no float record, got %+v", bv)
	}
	if want := 3.273; *bv.ValueFloat != want {
		t.Errorf("BoardVoltage: got %v, want %v", *bv.ValueFloat, want)
	}
}
