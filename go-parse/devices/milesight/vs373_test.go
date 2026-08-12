package milesight

import (
	"testing"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

func TestDecodeVS373_DetectionAndOccupancyV102(t *testing.T) {
	payload := []byte{
		// detection target (v1.0.2): status=2(in_bed), target=3(lying_down), use_time_now=300, use_time_today=1000
		0x07, 0xB0, 0x02, 0x03, 0x2C, 0x01, 0x00, 0xE8, 0x03, 0x00,
		// region occupancy (v1.0.2): count=3, bitmask=0b101 (region1 & region3 occupied, region2 vacant)
		0x0A, 0xB3, 0x03, 0x05, 0x00, 0x00, 0x00,
		// breathing: status=2(normal), rate=1650 (16.50 breaths/min)
		0x08, 0xB1, 0x02, 0x72, 0x06,
		// alarm: id=7, type=3(out_of_bed), status=1(triggered), region_id=2
		0x06, 0xFB, 0x07, 0x00, 0x03, 0x01, 0x02,
	}

	records := DecodeVS373(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	intCases := []struct {
		name string
		st   string
		want int64
	}{
		{"DetectionStatus", st.DetectionStatus, 2},
		{"TargetStatus", st.TargetStatus, 3},
		{"UseTimeNow", st.UseTimeNow, 300},
		{"UseTimeToday", st.UseTimeToday, 1000},
		{"RespiratoryStatus", st.RespiratoryStatus, 2},
		{"AlarmID", st.AlarmID, 7},
		{"AlarmType", st.AlarmType, 3},
		{"AlarmStatus", st.AlarmStatus, 1},
		{"AlarmRegionID", st.AlarmRegionID, 2},
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

	boolCases := []struct {
		name string
		st   string
		want bool
	}{
		{"Region1Occupancy", st.Region1Occupancy, true},
		{"Region2Occupancy", st.Region2Occupancy, false},
		{"Region3Occupancy", st.Region3Occupancy, true},
	}
	for _, c := range boolCases {
		r, ok := got[c.st]
		if !ok || r.ValueBool == nil {
			t.Fatalf("%s: no bool record, got %+v", c.name, r)
		}
		if *r.ValueBool != c.want {
			t.Errorf("%s: got %v, want %v", c.name, *r.ValueBool, c.want)
		}
	}

	if rate := got[st.RespiratoryRate]; rate.ValueFloat == nil || *rate.ValueFloat != 16.5 {
		t.Errorf("RespiratoryRate: got %+v, want 16.5", rate)
	}
}

func TestDecodeVS373_OccupancyV101Inverted(t *testing.T) {
	// region occupancy (v1.0.1): raw 0=occupied, 1=vacant (inverted vs v1.0.2)
	payload := []byte{0x04, 0xF9, 0x00, 0x01, 0x00, 0x01}

	records := DecodeVS373(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	want := map[string]bool{
		st.Region1Occupancy: true,  // raw 0x00 -> occupied
		st.Region2Occupancy: false, // raw 0x01 -> vacant
		st.Region3Occupancy: true,
		st.Region4Occupancy: false,
	}
	for sensorType, wantVal := range want {
		r, ok := got[sensorType]
		if !ok || r.ValueBool == nil {
			t.Fatalf("%s: no bool record, got %+v", sensorType, r)
		}
		if *r.ValueBool != wantVal {
			t.Errorf("%s: got %v, want %v", sensorType, *r.ValueBool, wantVal)
		}
	}
}

func TestDecodeVS373_OutOfBedV102(t *testing.T) {
	payload := []byte{
		// regions 1-3: 100s, 200s, 300s (3B LE each)
		0x0B, 0xB4, 0x64, 0x00, 0x00, 0xC8, 0x00, 0x00, 0x2C, 0x01, 0x00,
		// regions 4-6: 400s, 500s, 600s (3B LE each)
		0x0C, 0xB4, 0x90, 0x01, 0x00, 0xF4, 0x01, 0x00, 0x58, 0x02, 0x00,
	}

	records := DecodeVS373(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	want := map[string]int64{
		st.Region1OutOfBedTime: 100,
		st.Region2OutOfBedTime: 200,
		st.Region3OutOfBedTime: 300,
		st.Region4OutOfBedTime: 400,
		st.Region5OutOfBedTime: 500,
		st.Region6OutOfBedTime: 600,
	}
	for sensorType, wantVal := range want {
		r, ok := got[sensorType]
		if !ok || r.ValueInt == nil {
			t.Fatalf("%s: no int record, got %+v", sensorType, r)
		}
		if *r.ValueInt != wantVal {
			t.Errorf("%s: got %d, want %d", sensorType, *r.ValueInt, wantVal)
		}
	}
}
