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
	if temp := got[st.Temperature]; temp.ValueFloat == nil || *temp.ValueFloat != 25.3 {
		t.Errorf("Temperature: got %+v, want 25.3", temp)
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

	if temp := got[st.Temperature]; temp.ValueFloat == nil || *temp.ValueFloat != 18.5 {
		t.Errorf("Temperature: got %+v, want 18.5", temp)
	}
	if alarm := got[st.TemperatureAlarm]; alarm.ValueInt == nil || *alarm.ValueInt != 1 {
		t.Errorf("TemperatureAlarm: got %+v, want 1", alarm)
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
