package milesight

import (
	"testing"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

func TestDecodeAT101(t *testing.T) {
	payload := []byte{
		0x01, 0x75, 0x5A, // battery 90%
		0x03, 0x67, 0xFD, 0x00, // temperature 25.3C
		0x05, 0x00, 0x01, // device position: tilt
		0x07, 0x00, 0x01, // tamper status: uninstall
	}

	records := DecodeAT101(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	if bat := got[st.BatteryLevel]; bat.ValueFloat == nil || *bat.ValueFloat != 90 {
		t.Errorf("BatteryLevel: got %+v, want 90", bat)
	}
	if temp := got[st.AirTemp]; temp.ValueFloat == nil || *temp.ValueFloat != 25.3 {
		t.Errorf("AirTemp: got %+v, want 25.3", temp)
	}
	if pos := got[st.DevicePosition]; pos.ValueInt == nil || *pos.ValueInt != 1 {
		t.Errorf("DevicePosition: got %+v, want 1", pos)
	}
	if tamper := got[st.TamperStatus]; tamper.ValueInt == nil || *tamper.ValueInt != 1 {
		t.Errorf("TamperStatus: got %+v, want 1", tamper)
	}
}

func TestDecodeAT101_TemperatureAlarm(t *testing.T) {
	payload := []byte{0x83, 0x67, 0xB9, 0x00, 0x01} // temp=18.5C, alarm=abnormal

	records := DecodeAT101(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	if temp := got[st.AirTemp]; temp.ValueFloat == nil || *temp.ValueFloat != 18.5 {
		t.Errorf("AirTemp: got %+v, want 18.5", temp)
	}
	if alarm := got[st.AirTempAlarm]; alarm.ValueInt == nil || *alarm.ValueInt != 1 {
		t.Errorf("AirTempAlarm: got %+v, want 1", alarm)
	}
}

func TestDecodeAT101_Location(t *testing.T) {
	// lat=10.0, lon=-20.0, status: motion=2(moving), geofence=1(outside)
	payload := []byte{
		0x04, 0x88,
		0x80, 0x96, 0x98, 0x00,
		0x00, 0xD3, 0xCE, 0xFE,
		0x12,
	}

	records := DecodeAT101(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	if lat := got[st.Latitude]; lat.ValueFloat == nil || *lat.ValueFloat != 10.0 {
		t.Errorf("Latitude: got %+v, want 10.0", lat)
	}
	if lon := got[st.Longitude]; lon.ValueFloat == nil || *lon.ValueFloat != -20.0 {
		t.Errorf("Longitude: got %+v, want -20.0", lon)
	}
	if motion := got[st.MotionStatus]; motion.ValueInt == nil || *motion.ValueInt != 2 {
		t.Errorf("MotionStatus: got %+v, want 2", motion)
	}
	if geo := got[st.GeofenceStatus]; geo.ValueInt == nil || *geo.ValueInt != 1 {
		t.Errorf("GeofenceStatus: got %+v, want 1", geo)
	}
}

func TestDecodeAT101_LocationFullPrecision(t *testing.T) {
	// lat=-23.550520, lon=-46.633309 (Sao Paulo, 6 decimal digits) — proves
	// the int32/1e6 decode keeps full GPS precision, not just 1 decimal place.
	payload := []byte{
		0x04, 0x88,
		0xC8, 0xA5, 0x98, 0xFE, // lat_raw = -23550520
		0xA3, 0x6E, 0x38, 0xFD, // lon_raw = -46633309
		0x00,
	}

	records := DecodeAT101(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	if lat := got[st.Latitude]; lat.ValueFloat == nil || *lat.ValueFloat != -23.55052 {
		t.Errorf("Latitude: got %+v, want -23.55052", lat)
	}
	if lon := got[st.Longitude]; lon.ValueFloat == nil || *lon.ValueFloat != -46.633309 {
		t.Errorf("Longitude: got %+v, want -46.633309", lon)
	}
}

func TestDecodeAT101_LocationNoFix(t *testing.T) {
	// lat_raw=lon_raw=-1 (0xFFFFFFFF): the device's "no GPS/WiFi fix yet"
	// sentinel — reproduces the real payload pattern observed in production
	// (both fields -1e-6, plotting at Null Island). Must not be written.
	payload := []byte{
		0x04, 0x88,
		0xFF, 0xFF, 0xFF, 0xFF,
		0xFF, 0xFF, 0xFF, 0xFF,
		0x12,
	}

	records := DecodeAT101(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	if _, ok := got[st.Latitude]; ok {
		t.Errorf("Latitude: expected no record for no-fix sentinel, got %+v", got[st.Latitude])
	}
	if _, ok := got[st.Longitude]; ok {
		t.Errorf("Longitude: expected no record for no-fix sentinel, got %+v", got[st.Longitude])
	}
	// motion_status/geofence_status are independent of the GPS fix and
	// should still be written.
	if motion := got[st.MotionStatus]; motion.ValueInt == nil || *motion.ValueInt != 2 {
		t.Errorf("MotionStatus: got %+v, want 2", motion)
	}
	if geo := got[st.GeofenceStatus]; geo.ValueInt == nil || *geo.ValueInt != 1 {
		t.Errorf("GeofenceStatus: got %+v, want 1", geo)
	}
}

func TestDecodeAT101_LocationAlarmChannel(t *testing.T) {
	// same structure but on the 0x84 (geofence/alarm report) channel
	payload := []byte{
		0x84, 0x88,
		0x80, 0x96, 0x98, 0x00,
		0x00, 0xD3, 0xCE, 0xFE,
		0x12,
	}

	records := DecodeAT101(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	if lat := got[st.Latitude]; lat.ValueFloat == nil || *lat.ValueFloat != 10.0 {
		t.Errorf("Latitude: got %+v, want 10.0", lat)
	}
}
