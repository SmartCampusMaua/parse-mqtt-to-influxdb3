package milesight

import (
	"testing"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

func TestDecodeVS370(t *testing.T) {
	payload := []byte{
		0x01, 0x75, 0x5A, // battery 90%
		0x03, 0x00, 0x01, // occupancy: occupied
		0x04, 0x00, 0x01, // illuminance: bright
	}

	records := DecodeVS370(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
		if r.DeviceModel != "vs370" {
			t.Errorf("DeviceModel = %q, want vs370", r.DeviceModel)
		}
		if r.DeviceIndex != 0 {
			t.Errorf("%s: expected DeviceIndex 0 (single-instance sensor), got %d", r.SensorType, r.DeviceIndex)
		}
	}

	bat := got[st.BatteryLevel]
	if bat.ValueFloat == nil || *bat.ValueFloat != 90 {
		t.Errorf("BatteryLevel: got %+v, want 90", bat)
	}
	occ := got[st.OccupancyStatus]
	if occ.ValueBool == nil || !*occ.ValueBool {
		t.Errorf("OccupancyStatus: got %+v, want true", occ)
	}
	ill := got[st.IlluminanceStatus]
	if ill.ValueInt == nil || *ill.ValueInt != 1 {
		t.Errorf("IlluminanceStatus: got %+v, want 1", ill)
	}
}

func TestDecodeVS370_VacantAndDim(t *testing.T) {
	payload := []byte{
		0x03, 0x00, 0x00, // occupancy: vacant
		0x04, 0x00, 0x00, // illuminance: dim
	}

	records := DecodeVS370(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	occ := got[st.OccupancyStatus]
	if occ.ValueBool == nil || *occ.ValueBool {
		t.Errorf("OccupancyStatus: got %+v, want false", occ)
	}
	ill := got[st.IlluminanceStatus]
	if ill.ValueInt == nil || *ill.ValueInt != 0 {
		t.Errorf("IlluminanceStatus: got %+v, want 0", ill)
	}
}

func TestDecodeVS370_IlluminanceDisabled(t *testing.T) {
	payload := []byte{0x04, 0x00, 254} // illuminance: disable

	records := DecodeVS370(payload, "dev-1", "chirpstackv4", time.Now())

	if len(records) != 1 || records[0].ValueInt == nil || *records[0].ValueInt != 254 {
		t.Errorf("expected IlluminanceStatus=254 (disable), got %+v", records)
	}
}
