// UC100/UC300/UC501 shared byte mechanics.
//
// Reference: https://github.com/Milesight-IoT/SensorDecoders/tree/main/uc-series
//
// Unlike the other Milesight devices in this package, each UC-series RS485
// controller's MODBUS channel is genuinely variable-length: its size depends
// on a data_type byte embedded inside the entry itself, not on a static
// (channel,type) -> length table, so it cannot be tokenized by
// ParseMilesightTLV. This file exists only to walk the byte stream
// correctly; it makes no decision about what any channel *means* (no
// sensor_type is assigned here — see uc100.go/uc300.go/uc501.go).
//
// UC100/UC300 and UC501 disagree on the Modbus entry's wire shape itself,
// not just on value semantics: UC100/UC300 use a 3-byte header
// ([modbus_chn_id][data_length (unused)][data_type], sign in data_type's
// bit 7) and mark the entry with channel_id=0xFF, type=0x19. UC501 uses a
// 2-byte header ([modbus_chn_id][package_type], data_type in the low 3 bits
// of package_type, no sign bit) and marks it with channel_id=0xFF or 0x80,
// type=0x0E — 0x80 additionally carries one trailing alarm byte no other
// variant has. ParseUCTLV takes the "is this the Modbus marker" check and
// "how many bytes does this entry occupy" logic as parameters rather than
// hardcoding either shape, so one shared walker still covers all three
// models; each model's own file supplies its own two functions.
package milesight

import (
	"log"
	"math"
)

// UCEntry is one raw channel entry from a UC-series uplink, before any
// semantic interpretation.
type UCEntry struct {
	ChannelID   byte
	ChannelType byte
	// Data for a MODBUS entry is the model-specific header bytes followed by
	// the value bytes (and, for UC501's 0x80 channel, one trailing alarm
	// byte) — see each model's own ModbusValue decode function for the exact
	// shape.
	Data []byte
}

// modbusValueLen returns how many value bytes follow the data_type byte in a
// UC100/UC300 MODBUS channel entry, based on the type code in bits 0-6 of
// data_type (bit 7 is a sign flag, irrelevant to length). UC100 and UC300
// firmware disagree on what type codes 1 and 8-11 actually mean (see
// decodeUC100ModbusValue vs decodeUC300ModbusValue) but agree on byte length
// per code — which is all the tokenizer needs to walk past an entry.
func modbusValueLen(dataType byte) (n int, ok bool) {
	switch dataType & 0x7f {
	case 0, 1:
		return 1, true
	case 2, 3:
		return 2, true
	case 4, 5, 6, 7, 8, 9, 10, 11:
		return 4, true
	}
	return 0, false
}

// isUC100300ModbusEntry / uc100300ModbusEntryLen implement UC100/UC300's
// Modbus entry shape for ParseUCTLV.
func isUC100300ModbusEntry(ch, typ byte) bool {
	return ch == 0xFF && typ == 0x19
}

func uc100300ModbusEntryLen(ch byte, payload []byte, offset int) (int, bool) {
	if offset+3 > len(payload) {
		return 0, false
	}
	n, ok := modbusValueLen(payload[offset+2])
	if !ok {
		return 0, false
	}
	return 3 + n, true
}

// modbusValueLenV2 returns how many value bytes follow the two v2 header
// bytes in a UC100-v2-firmware MODBUS channel entry, based on the 5-bit type
// code in the low bits of the second header byte (bit 7 is sign, bits 5-6 a
// register offset — see decodeUC100ModbusValueV2). v2 adds int64/double
// (12-15) on top of v1's 0-11 range; types 8-11 still only carry 2 value
// bytes despite the 4-byte slot, matching v1 and the official decoder.
func modbusValueLenV2(dataType byte) (n int, ok bool) {
	switch dataType & 0x1f {
	case 0, 1:
		return 1, true
	case 2, 3:
		return 2, true
	case 4, 5, 6, 7, 8, 9, 10, 11:
		return 4, true
	case 12, 13, 14, 15:
		return 8, true
	}
	return 0, false
}

// isUC100V2ModbusEntry / uc100V2ModbusEntryLen implement UC100 v2 firmware's
// Modbus entry shape for ParseUCTLV: marker (0xF9, 0x73) instead of v1's
// (0xFF, 0x19), and a 2-byte packed header (chn_id+alarm, sign+reg_offset+
// data_type) instead of v1's 3-byte (chn_id, unused length, data_type).
// Reference: https://github.com/Milesight-IoT/SensorDecoders/blob/main/uc-series/uc100-v2/uc100-v2-decoder.js
func isUC100V2ModbusEntry(ch, typ byte) bool {
	return ch == 0xF9 && typ == 0x73
}

func uc100V2ModbusEntryLen(ch byte, payload []byte, offset int) (int, bool) {
	if offset+2 > len(payload) {
		return 0, false
	}
	n, ok := modbusValueLenV2(payload[offset+1])
	if !ok {
		return 0, false
	}
	return 2 + n, true
}

// ucSharedLengths covers fixed-length channel entries common across the
// UC-series beyond the universal Milesight attribute set already in
// genericTypeLengths (ipso_version, serial number, etc — reused as-is here).
var ucSharedLengths = map[[2]byte]int{
	{0xFF, 0x15}: 1, // MODBUS READ ERROR: modbus_chn_id
}

// ParseUCTLV tokenizes a UC-series uplink into channel entries.
//
// channelLengths covers model-specific fixed-length channels (UC300's
// GPIO/PT100/ADC hardware; UC501's GPIO/ADC/SDI-12; nil for UC100, which has
// none of those). isModbusEntry/modbusEntryLen supply this model's Modbus
// entry shape — see the file doc comment above for why that can't be a
// single hardcoded rule.
//
// Channel History (0x20,0xDC) and Modbus History (0x20,0xDD) records are
// nested variable-length structures not handled here — like an unrecognized
// channel/type, hitting one stops tokenization of the rest of the payload
// (logged, not a panic). This mirrors ParseMilesightTLV's existing
// "history/backfill out of scope today" limitation for other Milesight
// devices in this package.
func ParseUCTLV(
	payload []byte,
	channelLengths map[[2]byte]int,
	isModbusEntry func(ch, typ byte) bool,
	modbusEntryLen func(ch byte, payload []byte, offset int) (int, bool),
) []UCEntry {
	var out []UCEntry
	i := 0
	for i+1 < len(payload) {
		ch, typ := payload[i], payload[i+1]
		i += 2

		if isModbusEntry(ch, typ) {
			total, ok := modbusEntryLen(ch, payload, i)
			if !ok {
				log.Printf("[uc] unrecognized MODBUS entry at offset %d (ch=0x%02X type=0x%02X)", i, ch, typ)
				break
			}
			if i+total > len(payload) {
				log.Printf("[uc] truncated MODBUS entry at offset %d (need %d bytes)", i, total)
				break
			}
			out = append(out, UCEntry{ChannelID: ch, ChannelType: typ, Data: payload[i : i+total]})
			i += total
			continue
		}

		l, ok := channelLengths[[2]byte{ch, typ}]
		if !ok {
			l, ok = ucSharedLengths[[2]byte{ch, typ}]
		}
		if !ok {
			l, ok = genericTypeLengths[typ]
		}
		if !ok {
			log.Printf("[uc] unknown (ch=0x%02X type=0x%02X) at offset %d — stopping", ch, typ, i)
			break
		}
		if i+l > len(payload) {
			log.Printf("[uc] truncated at offset %d (need %d bytes)", i, l)
			break
		}
		out = append(out, UCEntry{ChannelID: ch, ChannelType: typ, Data: payload[i : i+l]})
		i += l
	}
	return out
}

// f16le decodes Milesight's non-IEEE754 half-float format (UC501 ADC v3,
// statistics fields elsewhere): 1 sign bit, 5 exponent bits (bias 25, not
// float16's usual 15), 10 mantissa bits, rounded to 2 decimal places same as
// the official decoder's toFixed(2).
func f16le(b []byte) float64 {
	bits := uint16(b[1])<<8 | uint16(b[0])
	sign := 1.0
	if bits>>15 != 0 {
		sign = -1.0
	}
	e := (bits >> 10) & 0x1f
	var m uint16
	if e == 0 {
		m = (bits & 0x3ff) << 1
	} else {
		m = (bits & 0x3ff) | 0x400
	}
	f := sign * float64(m) * math.Pow(2, float64(e)-25)
	return math.Round(f*100) / 100
}
