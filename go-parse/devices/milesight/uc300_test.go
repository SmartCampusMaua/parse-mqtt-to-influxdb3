package milesight

import (
	"testing"
	"time"
)

func TestDecodeUC300_ModbusTypes(t *testing.T) {
	payload := []byte{
		0xFF, 0x19, 0x00, 0x02, 0x02, 0xD2, 0x04, // chn1: uint16 1234
		0xFF, 0x19, 0x01, 0x02, 0x82, 0x9C, 0xFF, // chn2: int16 -100
		0xFF, 0x19, 0x02, 0x04, 0x05, 0x00, 0x00, 0xBC, 0x41, // chn3: float32 23.5
		0xFF, 0x19, 0x03, 0x01, 0x00, 0x01, // chn4: coil/on-off, on
		0xFF, 0x19, 0x04, 0x01, 0x81, 0xFB, // chn5: data_type=1 signed -> int8 -5
	}

	records := DecodeUC300(payload, "dev-1", "chirpstackv4", time.Now())
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
	// UC300 firmware treats data_type 1 as signed/unsigned 8-bit integer —
	// unlike UC100, which treats it as on/off. See uc100_test.go.
	if got := byChn[5]; got.valueType != "int" || got.i == nil || *got.i != -5 {
		t.Errorf("chn5 (data_type=1): got %+v, want int -5 (UC300 int8)", got)
	}

	for _, r := range records {
		if r.SensorType != "" {
			t.Errorf("chn%d: expected empty SensorType (resolved later from device_channels.json), got %q", r.ModbusChannel, r.SensorType)
		}
		if r.DeviceModel != "uc300" {
			t.Errorf("chn%d: DeviceModel = %q, want uc300", r.ModbusChannel, r.DeviceModel)
		}
	}
}

func TestDecodeUC300_HalfWordTypesSplitABFromCD(t *testing.T) {
	// UC300 splits 8/10 (first/AB half) from 9/11 (second/CD half) of the same
	// 4 value bytes — unlike UC100, which reads only the first half for all
	// four. value=AA BB CC DD: AB half LE=0xBBAA=48042, CD half LE=0xDDCC=56780.
	cases := []struct {
		dataType byte
		want     int64
	}{
		{0x08, 48042}, {0x0A, 48042}, // AB
		{0x09, 56780}, {0x0B, 56780}, // CD
	}
	for _, c := range cases {
		payload := []byte{0xFF, 0x19, 0x00, 0x04, c.dataType, 0xAA, 0xBB, 0xCC, 0xDD}
		records := DecodeUC300(payload, "dev-1", "chirpstackv4", time.Now())
		if len(records) != 1 || records[0].ValueInt == nil || *records[0].ValueInt != c.want {
			t.Errorf("data_type=0x%02X: got %+v, want int %d", c.dataType, records, c.want)
		}
	}
}

func TestDecodeUC300_ModbusReadError_NoRecord(t *testing.T) {
	payload := []byte{0xFF, 0x15, 0x00} // modbus_chn_id=0 -> channel 1, read error
	records := DecodeUC300(payload, "dev-1", "chirpstackv4", time.Now())
	if len(records) != 0 {
		t.Errorf("expected no records for a read-error entry, got %+v", records)
	}
}

func TestDecodeUC300_DecodesGPIOEntryThenParsesModbus(t *testing.T) {
	// Proves a fixed-hardware GPIO entry (now decoded via IOChannel, resolved
	// the same way as Modbus — see uc300.go) doesn't corrupt the parse of a
	// MODBUS entry that follows it in the same uplink.
	payload := []byte{
		0x03, 0x00, 0x01, // gpio_input_1: on
		0xFF, 0x19, 0x00, 0x02, 0x02, 0xD2, 0x04, // chn1: uint16 1234
	}

	records := DecodeUC300(payload, "dev-1", "chirpstackv4", time.Now())
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d: %+v", len(records), records)
	}
	if records[0].IOChannel != 0x03 || records[0].ValueBool == nil || *records[0].ValueBool != true {
		t.Errorf("got %+v, want io_channel 3 = true", records[0])
	}
	if records[1].ModbusChannel != 1 || records[1].ValueInt == nil || *records[1].ValueInt != 1234 {
		t.Errorf("got %+v, want modbus channel 1 = 1234", records[1])
	}
}

func TestDecodeUC300_FixedIOChannels(t *testing.T) {
	cases := []struct {
		name      string
		payload   []byte
		ioChannel int
		valueType string
		wantFloat float64
		wantInt   int64
		wantBool  bool
	}{
		{"gpio_input_on_off", []byte{0x04, 0x00, 0x01}, 0x04, "bool", 0, 0, true},
		{"gpio_input_counter", []byte{0x05, 0xC8, 0x2A, 0x00, 0x00, 0x00}, 0x05, "int", 0, 42, false},
		{"gpio_output_on_off", []byte{0x07, 0x01, 0x00}, 0x07, "bool", 0, 0, false},
		{"pt100", []byte{0x09, 0x67, 0xFD, 0x00}, 0x09, "float", 25.3, 0, false},                    // 253/10
		{"adc_current", []byte{0x0B, 0x02, 0x88, 0x13, 0x00, 0x00}, 0x0B, "float", 50.0, 0, false},  // 5000/100
		{"adc_voltage", []byte{0x0D, 0x02, 0x10, 0x27, 0x00, 0x00}, 0x0D, "float", 100.0, 0, false}, // 10000/100
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			records := DecodeUC300(c.payload, "dev-1", "chirpstackv4", time.Now())
			if len(records) != 1 {
				t.Fatalf("expected 1 record, got %d: %+v", len(records), records)
			}
			r := records[0]
			if r.IOChannel != c.ioChannel {
				t.Errorf("IOChannel = %d, want %d", r.IOChannel, c.ioChannel)
			}
			if r.SensorType != "" {
				t.Errorf("expected empty SensorType (resolved later from device_channels.json), got %q", r.SensorType)
			}
			switch c.valueType {
			case "bool":
				if r.ValueBool == nil || *r.ValueBool != c.wantBool {
					t.Errorf("got %+v, want bool %v", r, c.wantBool)
				}
			case "int":
				if r.ValueInt == nil || *r.ValueInt != c.wantInt {
					t.Errorf("got %+v, want int %d", r, c.wantInt)
				}
			case "float":
				if r.ValueFloat == nil || *r.ValueFloat != c.wantFloat {
					t.Errorf("got %+v, want float %v", r, c.wantFloat)
				}
			}
		})
	}
}

func TestDecodeUC300_StatisticsVariantNotDecoded(t *testing.T) {
	// 0xE2 (statistics: value+max+min+avg) is tokenized (walked past
	// correctly) but not decoded into a record — see file doc comment.
	payload := []byte{0x09, 0xE2, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	records := DecodeUC300(payload, "dev-1", "chirpstackv4", time.Now())
	if len(records) != 0 {
		t.Errorf("expected no records for the statistics variant, got %+v", records)
	}
}
