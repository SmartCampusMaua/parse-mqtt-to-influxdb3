package milesight

import (
	"testing"
	"time"
)

func TestDecodeUC100_ModbusTypes(t *testing.T) {
	payload := []byte{
		0xFF, 0x19, 0x00, 0x02, 0x02, 0xD2, 0x04, // chn1: uint16 1234
		0xFF, 0x19, 0x01, 0x02, 0x82, 0x9C, 0xFF, // chn2: int16 -100
		0xFF, 0x19, 0x02, 0x04, 0x05, 0x00, 0x00, 0xBC, 0x41, // chn3: float32 23.5
		0xFF, 0x19, 0x03, 0x01, 0x00, 0x01, // chn4: coil/on-off, on
		0xFF, 0x19, 0x04, 0x01, 0x01, 0x01, // chn5: type1 (=on/off on UC100), on
	}

	records := DecodeUC100(payload, "dev-1", "chirpstackv4", time.Now())
	byChn := map[int]struct {
		valueType string
		f         *float64
		i         *int64
		b         *bool
	}{}
	for _, r := range records {
		byChn[r.ModbusChannel] = struct {
			valueType string
			f         *float64
			i         *int64
			b         *bool
		}{r.ValueType, r.ValueFloat, r.ValueInt, r.ValueBool}
	}

	if len(records) != 5 {
		t.Fatalf("expected 5 records, got %d: %+v", len(records), records)
	}
	if got := byChn[1]; got.valueType != "int" || got.i == nil || *got.i != 1234 {
		t.Errorf("chn1: got %+v, want int 1234", got)
	}
	if got := byChn[2]; got.valueType != "int" || got.i == nil || *got.i != -100 {
		t.Errorf("chn2: got %+v, want int -100", got)
	}
	if got := byChn[3]; got.valueType != "float" || got.f == nil || *got.f != 23.5 {
		t.Errorf("chn3: got %+v, want float 23.5", got)
	}
	if got := byChn[4]; got.valueType != "bool" || got.b == nil || *got.b != true {
		t.Errorf("chn4: got %+v, want bool true", got)
	}
	// UC100 firmware treats data_type 1 the same as 0 (on/off) — unlike UC300,
	// which treats it as a signed/unsigned 8-bit integer. See uc300_test.go.
	if got := byChn[5]; got.valueType != "bool" || got.b == nil || *got.b != true {
		t.Errorf("chn5 (data_type=1): got %+v, want bool true (UC100 on/off)", got)
	}

	for _, r := range records {
		if r.SensorType != "" {
			t.Errorf("chn%d: expected empty SensorType (resolved later from device_channels.json), got %q", r.ModbusChannel, r.SensorType)
		}
		if r.DeviceModel != "uc100" {
			t.Errorf("chn%d: DeviceModel = %q, want uc100", r.ModbusChannel, r.DeviceModel)
		}
	}
}

func TestDecodeUC100_HalfWordTypesReadFirstHalfRegardless(t *testing.T) {
	// UC100 firmware reads only the first 2 of 4 value bytes for every one of
	// data_type 8/9/10/11 — unlike UC300, which splits 8/10 (first half) from
	// 9/11 (second half). value=AA BB CC DD, LE first half = 0xBBAA=48042.
	for _, dataType := range []byte{0x08, 0x09, 0x0A, 0x0B} {
		payload := []byte{0xFF, 0x19, 0x00, 0x04, dataType, 0xAA, 0xBB, 0xCC, 0xDD}
		records := DecodeUC100(payload, "dev-1", "chirpstackv4", time.Now())
		if len(records) != 1 || records[0].ValueInt == nil || *records[0].ValueInt != 48042 {
			t.Errorf("data_type=0x%02X: got %+v, want int 48042", dataType, records)
		}
	}
}

func TestDecodeUC100_ModbusReadError_NoRecord(t *testing.T) {
	payload := []byte{0xFF, 0x15, 0x00} // modbus_chn_id=0 -> channel 1, read error
	records := DecodeUC100(payload, "dev-1", "chirpstackv4", time.Now())
	if len(records) != 0 {
		t.Errorf("expected no records for a read-error entry, got %+v", records)
	}
}
