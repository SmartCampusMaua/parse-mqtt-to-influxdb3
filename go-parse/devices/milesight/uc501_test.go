package milesight

import (
	"testing"
	"time"
)

func TestDecodeUC501_Battery(t *testing.T) {
	payload := []byte{0x01, 0x75, 0x5A} // 90%
	records := DecodeUC501(payload, "dev-1", "chirpstackv4", time.Now())
	if len(records) != 1 || records[0].ValueFloat == nil || *records[0].ValueFloat != 90 {
		t.Errorf("got %+v, want battery_level 90", records)
	}
}

func TestDecodeUC501_GPIOAndPulseCounter(t *testing.T) {
	payload := []byte{
		0x03, 0x00, 0x01, // gpio 1 input: on
		0x04, 0x01, 0x00, // gpio 2 output: off
		0x03, 0xC8, 0x2A, 0x00, 0x00, 0x00, // gpio 1 as counter: 42 (site could wire ch3 as counter instead of input)
	}
	records := DecodeUC501(payload, "dev-1", "chirpstackv4", time.Now())
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d: %+v", len(records), records)
	}
	if records[0].IOChannel != 0x03 || records[0].ValueBool == nil || !*records[0].ValueBool {
		t.Errorf("gpio1 input: got %+v", records[0])
	}
	if records[1].IOChannel != 0x04 || records[1].ValueBool == nil || *records[1].ValueBool {
		t.Errorf("gpio2 output: got %+v", records[1])
	}
	if records[2].IOChannel != 0x03 || records[2].ValueInt == nil || *records[2].ValueInt != 42 {
		t.Errorf("gpio1 counter: got %+v", records[2])
	}
}

func TestDecodeUC501_AnalogInputV2(t *testing.T) {
	// value=1234(int16)/1000=1.234, min/max/avg ignored
	payload := []byte{0x05, 0x02, 0xD2, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	records := DecodeUC501(payload, "dev-1", "chirpstackv4", time.Now())
	if len(records) != 1 || records[0].IOChannel != 0x05 || records[0].ValueFloat == nil || *records[0].ValueFloat != 1.234 {
		t.Errorf("got %+v, want IOChannel=5 value=1.234", records)
	}
}

func TestDecodeUC501_AnalogInputV3AndAlarmShareIOChannel(t *testing.T) {
	// float16 encoding of 25.5: bits = sign(0) exp(?) ... just verify decode is self-consistent
	// by round-tripping through f16le with a known simple value: 0x00 0x00 -> 0.0
	normal := []byte{0x05, 0xE2, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	alarm := []byte{0x85, 0xE2, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	payload := append(append([]byte{}, normal...), alarm...)

	records := DecodeUC501(payload, "dev-1", "chirpstackv4", time.Now())
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d: %+v", len(records), records)
	}
	if records[0].IOChannel != 5 || records[1].IOChannel != 5 {
		t.Errorf("expected both normal and alarm variant to resolve to IOChannel=5, got %+v and %+v", records[0], records[1])
	}
}

func TestDecodeUC501_ModbusTypes(t *testing.T) {
	payload := []byte{
		0xFF, 0x0E, 0x0D, 0x02, 0xD2, 0x04, // wire_id=13, chn=13-6=7: uint16 1234
		0xFF, 0x0E, 0x0E, 0x00, 0x01, // wire_id=14, chn=14-6=8: on/off, on
		0xFF, 0x0E, 0x0F, 0x05, 0x00, 0x00, 0xBC, 0x41, // wire_id=15, chn=15-6=9: float32 23.5
	}
	records := DecodeUC501(payload, "dev-1", "chirpstackv4", time.Now())
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d: %+v", len(records), records)
	}
	if records[0].ModbusChannel != 7 || records[0].ValueInt == nil || *records[0].ValueInt != 1234 {
		t.Errorf("chn7: got %+v", records[0])
	}
	if records[1].ModbusChannel != 8 || records[1].ValueBool == nil || !*records[1].ValueBool {
		t.Errorf("chn8: got %+v", records[1])
	}
	if records[2].ModbusChannel != 9 || records[2].ValueFloat == nil || *records[2].ValueFloat != 23.5 {
		t.Errorf("chn9: got %+v", records[2])
	}
}

func TestDecodeUC501_ModbusAlarmChannelHasTrailingByte(t *testing.T) {
	// channel_id=0x80 carries a trailing alarm byte after the value — must
	// not be mistaken for the start of the next TLV entry.
	payload := []byte{
		0x80, 0x0E, 0x07, 0x02, 0xD2, 0x04, 0x01, // chn=1, uint16=1234, alarm byte=0x01 (ignored)
		0x01, 0x75, 0x5A, // battery 90% — must parse correctly after the alarm byte is consumed
	}
	records := DecodeUC501(payload, "dev-1", "chirpstackv4", time.Now())
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d: %+v", len(records), records)
	}
	if records[0].ModbusChannel != 1 || records[0].ValueInt == nil || *records[0].ValueInt != 1234 {
		t.Errorf("modbus: got %+v", records[0])
	}
	if records[1].ValueFloat == nil || *records[1].ValueFloat != 90 {
		t.Errorf("battery: got %+v", records[1])
	}
}

func TestDecodeUC501_ModbusReadError_NoRecord(t *testing.T) {
	payload := []byte{0xFF, 0x15, 0x07} // modbus_chn_id=7-6=1, read error
	records := DecodeUC501(payload, "dev-1", "chirpstackv4", time.Now())
	if len(records) != 0 {
		t.Errorf("expected no records for a read-error entry, got %+v", records)
	}
}

func TestDecodeUC501_SDI12TokenizedNotDecoded(t *testing.T) {
	payload := append([]byte{0x08, 0xDB, 0x00}, make([]byte, 36)...)
	records := DecodeUC501(payload, "dev-1", "chirpstackv4", time.Now())
	if len(records) != 0 {
		t.Errorf("expected no records for SDI-12 (tokenized only), got %+v", records)
	}
}
