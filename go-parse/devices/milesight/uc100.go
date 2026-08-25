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

// DecodeUC100 decodes UC100 binary payloads from LoRaWAN uplinks.
func DecodeUC100(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeUC100(ParseUCTLV(payload, nil), deviceID, provider, ts)
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
