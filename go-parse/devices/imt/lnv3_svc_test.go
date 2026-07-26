package imt

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

func TestDecodeLNV3SVC(t *testing.T) {
	// tag 0x0D x3: solenoid1=2000 (open), solenoid2=1000 (closed), solenoid3=1600 (open)
	// tag 0x0B: counter=42
	// tag 0x0C: board voltage raw=3300 (-> 3.3 V)
	payload := []byte{
		0x0D, 0x07, 0xD0,
		0x0D, 0x03, 0xE8,
		0x0D, 0x06, 0x40,
		0x0B, 0x00, 0x00, 0x2A,
		0x0C, 0x0C, 0xE4,
	}

	records := DecodeLNV3SVC(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	cases := []struct {
		name     string
		sv       string
		wantBool bool
	}{
		{"SV1", st.SV1, true},
		{"SV2", st.SV2, false},
		{"SV3", st.SV3, true},
	}
	for _, c := range cases {
		r, ok := got[c.sv]
		if !ok {
			t.Fatalf("%s: no record for sensor_type %q", c.name, c.sv)
		}
		if r.ValueType != "bool" || r.ValueBool == nil {
			t.Fatalf("%s: expected bool record, got %+v", c.name, r)
		}
		if *r.ValueBool != c.wantBool {
			t.Errorf("%s: got %v, want %v", c.name, *r.ValueBool, c.wantBool)
		}
	}

	pc, ok := got[st.PulseCount]
	if !ok || pc.ValueInt == nil {
		t.Fatalf("PulseCount: no int record, got %+v", pc)
	}
	if *pc.ValueInt != 42 {
		t.Errorf("PulseCount: got %d, want 42", *pc.ValueInt)
	}

	bv, ok := got[st.BoardVoltage]
	if !ok || bv.ValueFloat == nil {
		t.Fatalf("BoardVoltage: no float record, got %+v", bv)
	}
	if *bv.ValueFloat != 3.3 {
		t.Errorf("BoardVoltage: got %v, want 3.3", *bv.ValueFloat)
	}
}

func TestDecodeLNV3SVC_RealPayload(t *testing.T) {
	payload, err := base64.StdEncoding.DecodeString("CwBlJw0AAA0AAA0AAAwM0w==")
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}

	records := DecodeLNV3SVC(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	for _, sv := range []string{st.SV1, st.SV2, st.SV3} {
		r, ok := got[sv]
		if !ok || r.ValueBool == nil {
			t.Fatalf("%s: no bool record, got %+v", sv, r)
		}
		if *r.ValueBool != false {
			t.Errorf("%s: got %v, want false", sv, *r.ValueBool)
		}
	}

	pc, ok := got[st.PulseCount]
	if !ok || pc.ValueInt == nil {
		t.Fatalf("PulseCount: no int record, got %+v", pc)
	}
	if *pc.ValueInt != 25895 {
		t.Errorf("PulseCount: got %d, want 25895", *pc.ValueInt)
	}

	bv, ok := got[st.BoardVoltage]
	if !ok || bv.ValueFloat == nil {
		t.Fatalf("BoardVoltage: no float record, got %+v", bv)
	}
	if want := 3.283; *bv.ValueFloat != want {
		t.Errorf("BoardVoltage: got %v, want %v", *bv.ValueFloat, want)
	}
}
