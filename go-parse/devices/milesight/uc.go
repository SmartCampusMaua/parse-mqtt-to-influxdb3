// UC100/UC300 shared byte mechanics.
//
// Reference: https://github.com/Milesight-IoT/SensorDecoders/tree/main/uc-series
//
// Unlike the other Milesight devices in this package, the UC100/UC300 MODBUS
// channel (0xFF,0x19) is genuinely variable-length: its size depends on a
// data_type byte embedded inside the entry itself, not on a static
// (channel,type) -> length table, so it cannot be tokenized by
// ParseMilesightTLV. This file exists only to walk the byte stream correctly;
// it makes no decision about what any channel *means* (no sensor_type is
// assigned here — see uc100.go/uc300.go).
package milesight

import (
	"log"
)

// UCEntry is one raw channel entry from a UC100/UC300 uplink, before any
// semantic interpretation.
type UCEntry struct {
	ChannelID   byte
	ChannelType byte
	// Data for a MODBUS entry (ChannelID==0xFF, ChannelType==0x19) is
	// [modbus_chn_id][data_length (unused, see below)][data_type][value...].
	Data []byte
}

// modbusValueLen returns how many value bytes follow the data_type byte in a
// MODBUS channel entry, based on the type code in bits 0-6 of data_type (bit
// 7 is a sign flag, irrelevant to length). UC100 and UC300 firmware disagree
// on what type codes 1 and 8-11 actually mean (see decodeUC100ModbusValue vs
// decodeUC300ModbusValue) but agree on byte length per code — which is all
// the tokenizer needs to walk past an entry.
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

// ucSharedLengths covers fixed-length channel entries common to both UC100
// and UC300 beyond the universal Milesight attribute set already in
// genericTypeLengths (ipso_version, serial number, etc — reused as-is here).
var ucSharedLengths = map[[2]byte]int{
	{0xFF, 0x15}: 1, // MODBUS READ ERROR: modbus_chn_id
}

// ParseUCTLV tokenizes a UC100/UC300 uplink into channel entries.
// channelLengths covers model-specific fixed-length channels (UC300's
// GPIO/PT100/ADC hardware; nil for UC100, which has none of those).
//
// Channel History (0x20,0xDC) and Modbus History (0x20,0xDD) records are
// nested variable-length structures not handled here — like an unrecognized
// channel/type, hitting one stops tokenization of the rest of the payload
// (logged, not a panic). This mirrors ParseMilesightTLV's existing
// "history/backfill out of scope today" limitation for other Milesight
// devices in this package.
func ParseUCTLV(payload []byte, channelLengths map[[2]byte]int) []UCEntry {
	var out []UCEntry
	i := 0
	for i+1 < len(payload) {
		ch, typ := payload[i], payload[i+1]
		i += 2

		if ch == 0xFF && typ == 0x19 {
			if i+3 > len(payload) {
				log.Printf("[uc] truncated MODBUS header at offset %d", i)
				break
			}
			dataType := payload[i+2]
			n, ok := modbusValueLen(dataType)
			if !ok {
				log.Printf("[uc] unknown MODBUS data_type 0x%02X at offset %d", dataType, i+2)
				break
			}
			total := 3 + n
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
