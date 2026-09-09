package imt

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

func TestDecodeLNV3SVC(t *testing.T) {
	// tag 0x0D x3: solenoid1=2000, solenoid2=1000, solenoid3=1600 (raw readings)
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
	byIndex := map[int]record.SensorDataRecord{} // solenoid_valve_raw: DeviceIndex -> record
	got := map[string]record.SensorDataRecord{}  // everything else: SensorType -> record
	for _, r := range records {
		if r.SensorType == st.SolenoidValveRaw {
			byIndex[r.DeviceIndex] = r
			continue
		}
		got[r.SensorType] = r
	}

	cases := []struct {
		name        string
		deviceIndex int
		want        int64
	}{
		{"valve 1", 1, 2000},
		{"valve 2", 2, 1000},
		{"valve 3", 3, 1600},
	}
	for _, c := range cases {
		r, ok := byIndex[c.deviceIndex]
		if !ok || r.ValueInt == nil {
			t.Fatalf("%s: no int record, got %+v", c.name, r)
		}
		if *r.ValueInt != c.want {
			t.Errorf("%s: got %d, want %d", c.name, *r.ValueInt, c.want)
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
	byIndex := map[int]record.SensorDataRecord{}
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		if r.SensorType == st.SolenoidValveRaw {
			byIndex[r.DeviceIndex] = r
			continue
		}
		got[r.SensorType] = r
	}

	for _, deviceIndex := range []int{1, 2, 3} {
		r, ok := byIndex[deviceIndex]
		if !ok || r.ValueInt == nil {
			t.Fatalf("device_index %d: no int record, got %+v", deviceIndex, r)
		}
		if *r.ValueInt != 0 {
			t.Errorf("device_index %d: got %d, want 0", deviceIndex, *r.ValueInt)
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
