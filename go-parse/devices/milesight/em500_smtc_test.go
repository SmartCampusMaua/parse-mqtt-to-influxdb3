package milesight

import (
	"testing"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

func TestDecodeEM500SMTC(t *testing.T) {
	// battery=85%, temperature=23.5C, moisture(new)=45.5%, EC=1200 uS/cm
	payload := []byte{
		0x01, 0x75, 0x55,
		0x03, 0x67, 0xEB, 0x00,
		0x04, 0xCA, 0xC6, 0x11,
		0x05, 0x7F, 0xB0, 0x04,
	}

	records := DecodeEM500SMTC(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	bat, ok := got[st.BatteryLevel]
	if !ok || bat.ValueFloat == nil || *bat.ValueFloat != 85 {
		t.Errorf("BatteryLevel: got %+v, want 85", bat)
	}

	temp, ok := got[st.SoilTemp]
	if !ok || temp.ValueFloat == nil || *temp.ValueFloat != 23.5 {
		t.Errorf("SoilTemp: got %+v, want 23.5", temp)
	}

	moist, ok := got[st.SoilMoisture]
	if !ok || moist.ValueFloat == nil || *moist.ValueFloat != 45.5 {
		t.Errorf("SoilMoisture: got %+v, want 45.5", moist)
	}

	ec, ok := got[st.ElectricalConductivity]
	if !ok || ec.ValueInt == nil || *ec.ValueInt != 1200 {
		t.Errorf("ElectricalConductivity: got %+v, want 1200", ec)
	}
}

func TestDecodeEM500SMTC_TempMutationAlarm(t *testing.T) {
	// temp=21.0C, mutation=5.0C, alarm_type=2 (mutation alarm)
	payload := []byte{0x83, 0xD7, 0xD2, 0x00, 0x32, 0x00, 0x02}

	records := DecodeEM500SMTC(payload, "dev-1", "chirpstackv4", time.Now())

	st := record.ST
	got := map[string]record.SensorDataRecord{}
	for _, r := range records {
		got[r.SensorType] = r
	}

	if temp := got[st.SoilTemp]; temp.ValueFloat == nil || *temp.ValueFloat != 21.0 {
		t.Errorf("SoilTemp: got %+v, want 21.0", temp)
	}
	if mut := got[st.TemperatureMutation]; mut.ValueFloat == nil || *mut.ValueFloat != 5.0 {
		t.Errorf("TemperatureMutation: got %+v, want 5.0", mut)
	}
	if alarm := got[st.SoilTempAlarm]; alarm.ValueInt == nil || *alarm.ValueInt != 2 {
		t.Errorf("SoilTempAlarm: got %+v, want 2", alarm)
	}
}
