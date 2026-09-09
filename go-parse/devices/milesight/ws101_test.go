package milesight

import (
	"testing"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

func TestDecodeWS101Variants_SameWireFormatDifferentDeviceModel(t *testing.T) {
	payload := []byte{
		0x01, 0x75, 0x5A, // battery 90%
		0xFF, 0x2E, 0x01, // press_type: single
	}

	cases := []struct {
		name   string
		decode func([]byte, string, string, time.Time) []record.SensorDataRecord
		wantDM string
	}{
		{"bare", DecodeWS101, "ws101"},
		{"sos", DecodeWS101SOS, "ws101_sos"},
		{"scene", DecodeWS101Scene, "ws101_scene"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			records := c.decode(payload, "dev-1", "chirpstackv4", time.Now())
			if len(records) != 2 {
				t.Fatalf("expected 2 records (battery_level, press_type), got %d: %+v", len(records), records)
			}
			for _, r := range records {
				if r.DeviceModel != c.wantDM {
					t.Errorf("%s: DeviceModel = %q, want %q", r.SensorType, r.DeviceModel, c.wantDM)
				}
			}
			st := record.ST
			got := map[string]record.SensorDataRecord{}
			for _, r := range records {
				got[r.SensorType] = r
			}
			if bat := got[st.BatteryLevel]; bat.ValueFloat == nil || *bat.ValueFloat != 90 {
				t.Errorf("BatteryLevel: got %+v, want 90", bat)
			}
			if press := got[st.PressType]; press.ValueInt == nil || *press.ValueInt != 1 {
				t.Errorf("PressType: got %+v, want 1", press)
			}
		})
	}
}
