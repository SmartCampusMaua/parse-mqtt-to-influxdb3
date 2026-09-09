// Reference: https://github.com/Milesight-IoT/SensorDecoders/blob/main/uc-series/uc100/uc100-decoder.js
//
// UC100 is Modbus-only (no GPIO/PT100/ADC hardware, unlike UC300).
package milesight

import (
	"log"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

const dmUC100 = "uc100"

// isUC100ModbusEntry / uc100ModbusEntryLen recognize either UC100 wire
// format: older firmware's (0xFF,0x19)/3-byte-header shape, or v2 firmware's
// (0xF9,0x73)/2-byte-header shape. isModbusEntry already pins down which
// shape matched by the time modbusEntryLen runs, so branching on ch alone is
// enough — see decodeUC100's dispatch for the same distinction.
func isUC100ModbusEntry(ch, typ byte) bool {
	return isUC100300ModbusEntry(ch, typ) || isUC100V2ModbusEntry(ch, typ)
}

func uc100ModbusEntryLen(ch byte, payload []byte, offset int) (int, bool) {
	if ch == 0xF9 {
		return uc100V2ModbusEntryLen(ch, payload, offset)
	}
	return uc100300ModbusEntryLen(ch, payload, offset)
}

// DecodeUC100 decodes UC100 binary payloads from LoRaWAN uplinks.
func DecodeUC100(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeUC100(ParseUCTLV(payload, nil, isUC100ModbusEntry, uc100ModbusEntryLen), deviceID, provider, ts)
}

// decodeUC100 extracts Modbus channel readings from a UC100 uplink.
//
// A UC100 decoder cannot know what a Modbus channel *means* (that's entirely
// site config — see device_channels.json/sensors.json) so these records
// carry ModbusChannel instead of SensorType; the caller (main.go) resolves
// SensorType/SensorID from it post-decode, dropping anything with no config
// entry rather than writing it under a made-up name.
func decodeUC100(entries []UCEntry, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	var out []record.SensorDataRecord
	for _, e := range entries {
		switch {
		case e.ChannelID == 0xFF && e.ChannelType == 0x19:
			r, ok := decodeUC100ModbusValue(e.Data, deviceID, provider, ts)
			if !ok {
				continue
			}
			out = append(out, r)
		case e.ChannelID == 0xF9 && e.ChannelType == 0x73:
			r, ok := decodeUC100ModbusValueV2(e.Data, deviceID, provider, ts)
			if !ok {
				continue
			}
			out = append(out, r)
		case e.ChannelID == 0xFF && e.ChannelType == 0x15:
			chn := int(e.Data[0]) + 1
			log.Printf("[uc100] device_id=%s modbus_channel=%d read error reported by device", deviceID, chn)
		}
	}
	return out
}

// decodeUC100ModbusValue decodes one MODBUS channel entry's value bytes.
// data is [modbus_chn_id][data_length (unused)][data_type][value...], as
// tokenized by ParseUCTLV. Ported byte-for-byte from the official decoder's
// MODBUS switch (type codes 8/9/10/11 all read only the first 2 of the 4
// value bytes on UC100 — this differs from UC300, see decodeUC300ModbusValue).
func decodeUC100ModbusValue(data []byte, deviceID, provider string, ts time.Time) (record.SensorDataRecord, bool) {
	chn := int(data[0]) + 1
	dataType := data[2]
	sign := dataType&0x80 != 0
	typ := dataType & 0x7f
	val := data[3:]

	r := record.SensorDataRecord{
		DeviceModel:   dmUC100,
		DeviceID:      deviceID,
		Provider:      provider,
		Timestamp:     ts,
		ModbusChannel: chn,
	}

	switch typ {
	case 0, 1:
		v := val[0] != 0
		r.ValueType, r.ValueBool = "bool", &v
	case 2, 3:
		v := int64(le16(val))
		if sign {
			v = int64(int16(le16(val)))
		}
		r.ValueType, r.ValueInt = "int", &v
	case 4, 6:
		v := int64(le32(val))
		if sign {
			v = int64(int32(le32(val)))
		}
		r.ValueType, r.ValueInt = "int", &v
	case 8, 9, 10, 11:
		u := le16(val[:2])
		v := int64(u)
		if sign {
			v = int64(int16(u))
		}
		r.ValueType, r.ValueInt = "int", &v
	case 5, 7:
		v := f32le(val)
		r.ValueType, r.ValueFloat = "float", &v
	default:
		return record.SensorDataRecord{}, false
	}

	return r, true
}

// decodeUC100ModbusValueV2 decodes one MODBUS channel entry from UC100 v2
// firmware. data is [value_1][value_2][value...]: value_1's low 6 bits are
// modbus_chn_id-1 (bits 6-7 an alarm flag, not surfaced here — same as v1's
// separate MODBUS READ ERROR channel being the only alarm signal handled
// today); value_2's bit 7 is sign, bits 5-6 a register offset, bits 0-4 the
// data type. Only offset 0 (one register per configured channel) is
// supported — device_channels.json has no concept of a second register per
// channel, so anything else is dropped. Ported from the official v2
// decoder's MODBUS switch, which adds int64/double (12-15) over v1's 0-11.
// https://github.com/Milesight-IoT/SensorDecoders/blob/main/uc-series/uc100-v2/uc100-v2-decoder.js
func decodeUC100ModbusValueV2(data []byte, deviceID, provider string, ts time.Time) (record.SensorDataRecord, bool) {
	chn := int(data[0]&0x3f) + 1
	regOffset := (data[1] >> 5) & 0x03
	if regOffset != 0 {
		log.Printf("[uc100] device_id=%s modbus_channel=%d reg_offset=%d unsupported — dropping", deviceID, chn, regOffset)
		return record.SensorDataRecord{}, false
	}
	sign := data[1]&0x80 != 0
	typ := data[1] & 0x1f
	val := data[2:]

	r := record.SensorDataRecord{
		DeviceModel:   dmUC100,
		DeviceID:      deviceID,
		Provider:      provider,
		Timestamp:     ts,
		ModbusChannel: chn,
	}

	switch typ {
	case 0, 1:
		v := val[0] != 0
		r.ValueType, r.ValueBool = "bool", &v
	case 2, 3:
		v := int64(le16(val))
		if sign {
			v = int64(int16(le16(val)))
		}
		r.ValueType, r.ValueInt = "int", &v
	case 4, 6:
		v := int64(le32(val))
		if sign {
			v = int64(int32(le32(val)))
		}
		r.ValueType, r.ValueInt = "int", &v
	case 8, 9, 10, 11:
		u := le16(val[:2])
		v := int64(u)
		if sign {
			v = int64(int16(u))
		}
		r.ValueType, r.ValueInt = "int", &v
	case 5, 7:
		v := f32le(val)
		r.ValueType, r.ValueFloat = "float", &v
	case 12, 14:
		v := int64(le64(val))
		r.ValueType, r.ValueInt = "int", &v
	case 13, 15:
		v := f64le(val)
		r.ValueType, r.ValueFloat = "float", &v
	default:
		return record.SensorDataRecord{}, false
	}

	return r, true
}
