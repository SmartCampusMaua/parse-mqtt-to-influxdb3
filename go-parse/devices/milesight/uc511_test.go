package milesight

import (
	"testing"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

func TestDecodeUC511_AllChannels(t *testing.T) {
	payload := []byte{
		0x01, 0x75, 0x5A, // battery 90%
		0x03, 0x01, 0x01, // valve 1: open
		0x05, 0x01, 0x00, // valve 2: closed
		0x04, 0xC8, 0x2A, 0x00, 0x00, 0x00, // valve 1 pulse: 42
		0x06, 0xC8, 0x64, 0x00, 0x00, 0x00, // valve 2 pulse: 100
		0x07, 0x01, 0x01, // gpio 1: on
		0x08, 0x01, 0x00, // gpio 2: off
		0x09, 0x7B, 0xDC, 0x05, // pressure: 1500
		0xB9, 0x7B, 0x01, // pressure_sensor_status: error
	}

	records := DecodeUC511(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	byType := map[string]record.SensorDataRecord{}
	byTypeIndex := map[string]record.SensorDataRecord{}
	for _, r := range records {
		if r.DeviceIndex != 0 {
			byTypeIndex[r.SensorType+"#"+string(rune('0'+r.DeviceIndex))] = r
			continue
		}
		byType[r.SensorType] = r
	}

	bat := byType[st.BatteryLevel]
	if bat.ValueFloat == nil || *bat.ValueFloat != 90 {
		t.Errorf("BatteryLevel: got %+v, want 90", bat)
	}

	v1 := byTypeIndex[st.SolenoidValveStatus+"#1"]
	if v1.ValueBool == nil || !*v1.ValueBool {
		t.Errorf("valve 1 status: got %+v, want true", v1)
	}
	v2 := byTypeIndex[st.SolenoidValveStatus+"#2"]
	if v2.ValueBool == nil || *v2.ValueBool {
		t.Errorf("valve 2 status: got %+v, want false", v2)
	}

	p1 := byTypeIndex[st.PulseCount+"#1"]
	if p1.ValueInt == nil || *p1.ValueInt != 42 {
		t.Errorf("valve 1 pulse: got %+v, want 42", p1)
	}
	p2 := byTypeIndex[st.PulseCount+"#2"]
	if p2.ValueInt == nil || *p2.ValueInt != 100 {
		t.Errorf("valve 2 pulse: got %+v, want 100", p2)
	}

	g1 := byTypeIndex[st.DigitalInput+"#1"]
	if g1.ValueBool == nil || !*g1.ValueBool {
		t.Errorf("gpio 1: got %+v, want true", g1)
	}
	g2 := byTypeIndex[st.DigitalInput+"#2"]
	if g2.ValueBool == nil || *g2.ValueBool {
		t.Errorf("gpio 2: got %+v, want false", g2)
	}

	pressure := byType[st.WaterPressure]
	if pressure.ValueInt == nil || *pressure.ValueInt != 1500 {
		t.Errorf("WaterPressure: got %+v, want 1500", pressure)
	}

	fail := byType[st.PressureSensorFailStatus]
	if fail.ValueBool == nil || !*fail.ValueBool {
		t.Errorf("PressureSensorFailStatus: got %+v, want true", fail)
	}

	for _, r := range records {
		if r.SensorType != st.SolenoidValveStatus && r.SensorType != st.PulseCount && r.SensorType != st.DigitalInput && r.DeviceIndex != 0 {
			t.Errorf("unexpected DeviceIndex on unindexed sensor_type %q: %+v", r.SensorType, r)
		}
		if r.DeviceModel != "uc511" {
			t.Errorf("DeviceModel = %q, want uc511", r.DeviceModel)
		}
	}
}

func TestDecodeUC511_SkipsDelayControlResultEcho(t *testing.T) {
	// valve raw byte 0xFF is a downlink command-result echo, not a status
	// reading — must not produce a solenoid_valve_status record.
	payload := []byte{0x03, 0x01, 0xFF}

	records := DecodeUC511(payload, "dev-1", "chirpstackv4", time.Now())

	if len(records) != 0 {
		t.Errorf("expected no records for a delay-control result echo, got %+v", records)
	}
}

func TestDecodeUC511_PressureSensorOK(t *testing.T) {
	payload := []byte{0xB9, 0x7B, 0x00}

	records := DecodeUC511(payload, "dev-1", "chirpstackv4", time.Now())

	if len(records) != 1 || records[0].ValueBool == nil || *records[0].ValueBool {
		t.Errorf("expected PressureSensorFailStatus=false, got %+v", records)
	}
}
