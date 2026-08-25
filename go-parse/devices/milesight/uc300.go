// Reference: https://github.com/Milesight-IoT/SensorDecoders/blob/main/uc-series/uc300/uc300-decoder.js
//
// UC300's fixed-hardware GPIO/PT100/ADC I/O is resolved exactly like its
// Modbus channel: what a channel_id (3-14) is wired to is site config, not
// something this decoder can name — see record.SensorDataRecord.IOChannel
// and resolveChannelRecord (main.go). Only the *plain instantaneous
// reading* variant of each channel is decoded (data_type 0x00/0xC8/0x01/
// 0x67/0x02); the *statistics* variant (0xE2: value+max+min+avg packed as
// four float16s) is tokenized — so it doesn't corrupt the parse of what
// follows — but not decoded, since a single sensor_data point can only
// carry one value per (sensor_type, timestamp), same reasoning as VS373's
// WiFi scan results and AT101's history channel.
package milesight

import (
	"log"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

const dmUC300 = "uc300"

// DecodeUC300 decodes UC300 binary payloads from LoRaWAN uplinks.
func DecodeUC300(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeUC300(ParseUCTLV(payload, uc300ChannelLengths), deviceID, provider, ts)
}

// uc300ChannelLengths: byte lengths only, sourced directly from the official
// decoder.js gpio_input_chns/gpio_output_chns/pt100_chns/ai_chns/av_chns
// tables. No semantic meaning is assigned to any of these entries.
var uc300ChannelLengths = map[[2]byte]int{
	{0x03, 0x00}: 1, {0x04, 0x00}: 1, {0x05, 0x00}: 1, {0x06, 0x00}: 1, // gpio input on/off
	{0x03, 0xC8}: 4, {0x04, 0xC8}: 4, {0x05, 0xC8}: 4, {0x06, 0xC8}: 4, // gpio input as counter
	{0x07, 0x01}: 1, {0x08, 0x01}: 1, // gpio output on/off
	{0x09, 0x67}: 2, {0x0A, 0x67}: 2, // pt100
	{0x0B, 0x02}: 4, {0x0C, 0x02}: 4, // adc (current loop)
	{0x0D, 0x02}: 4, {0x0E, 0x02}: 4, // adc (voltage)
	{0x09, 0xE2}: 8, {0x0A, 0xE2}: 8, // pt100 statistics
	{0x0B, 0xE2}: 8, {0x0C, 0xE2}: 8, // adc statistics
	{0x0D, 0xE2}: 8, {0x0E, 0xE2}: 8, // adv statistics
}

// decodeUC300 extracts Modbus and fixed-I/O channel readings from a UC300
// uplink.
//
// A UC300 decoder cannot know what any channel *means* (that's entirely site
// config — see device_channels.json/sensors.json) so these records carry
// ModbusChannel or IOChannel instead of SensorType; the caller (main.go)
// resolves SensorType/SensorID from it post-decode, dropping anything with
// no config entry rather than writing it under a made-up name.
func decodeUC300(entries []UCEntry, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	var out []record.SensorDataRecord
	for _, e := range entries {
		switch {
		case e.ChannelID == 0xFF && e.ChannelType == 0x19:
			r, ok := decodeUC300ModbusValue(e.Data, deviceID, provider, ts)
			if !ok {
				continue
			}
			out = append(out, r)
		case e.ChannelID == 0xFF && e.ChannelType == 0x15:
			chn := int(e.Data[0]) + 1
			log.Printf("[uc300] device_id=%s modbus_channel=%d read error reported by device", deviceID, chn)
		default:
			r, ok := decodeUC300IOValue(e.ChannelID, e.ChannelType, e.Data, deviceID, provider, ts)
			if !ok {
				continue
			}
			out = append(out, r)
		}
	}
	return out
}

// decodeUC300IOValue decodes one fixed-hardware GPIO/PT100/ADC entry's plain
// (non-statistics) reading. channelID is the raw wire channel_id (3-14),
// used as-is for IOChannel — unlike Modbus, the official decoder does not
// +1 these. Byte lengths ported from uc300ChannelLengths/the official
// decoder.js; ok=false for anything not a recognized plain-reading pair
// (including the 0xE2 statistics variant — see file doc comment).
func decodeUC300IOValue(channelID, channelType byte, data []byte, deviceID, provider string, ts time.Time) (record.SensorDataRecord, bool) {
	r := record.SensorDataRecord{
		DeviceModel: dmUC300,
		DeviceID:    deviceID,
		Provider:    provider,
		Timestamp:   ts,
		IOChannel:   int(channelID),
	}

	switch {
	case (channelID >= 0x03 && channelID <= 0x06) && channelType == 0x00: // gpio input on/off
		v := data[0] != 0
		r.ValueType, r.ValueBool = "bool", &v
	case (channelID >= 0x03 && channelID <= 0x06) && channelType == 0xC8: // gpio input as counter
		v := int64(le32(data))
		r.ValueType, r.ValueInt = "int", &v
	case (channelID == 0x07 || channelID == 0x08) && channelType == 0x01: // gpio output on/off
		v := data[0] != 0
		r.ValueType, r.ValueBool = "bool", &v
	case (channelID == 0x09 || channelID == 0x0A) && channelType == 0x67: // pt100
		v := float64(int16(le16(data))) / 10
		r.ValueType, r.ValueFloat = "float", &v
	case (channelID >= 0x0B && channelID <= 0x0E) && channelType == 0x02: // adc current(0x0B/0x0C) or voltage(0x0D/0x0E)
		v := float64(le32(data)) / 100
		r.ValueType, r.ValueFloat = "float", &v
	default:
		return record.SensorDataRecord{}, false
	}

	return r, true
}

// decodeUC300ModbusValue decodes one MODBUS channel entry's value bytes.
// data is [modbus_chn_id][data_length (unused)][data_type][value...], as
// tokenized by ParseUCTLV. Ported byte-for-byte from the official decoder's
// MODBUS switch — note UC300 differs from UC100 on type codes 1 (int8/uint8
// here, vs UC100's on/off) and 8-11 (AB/CD half-word split here, vs UC100
// reading the first half for all four).
func decodeUC300ModbusValue(data []byte, deviceID, provider string, ts time.Time) (record.SensorDataRecord, bool) {
	chn := int(data[0]) + 1
	dataType := data[2]
	sign := dataType&0x80 != 0
	typ := dataType & 0x7f
	val := data[3:]

	r := record.SensorDataRecord{
		DeviceModel:   dmUC300,
		DeviceID:      deviceID,
		Provider:      provider,
		Timestamp:     ts,
		ModbusChannel: chn,
	}

	switch typ {
	case 0:
		v := val[0] != 0
		r.ValueType, r.ValueBool = "bool", &v
	case 1:
		v := int64(val[0])
		if sign {
			v = int64(int8(val[0]))
		}
		r.ValueType, r.ValueInt = "int", &v
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
	case 8, 10: // AB: first half-word
		u := le16(val[:2])
		v := int64(u)
		if sign {
			v = int64(int16(u))
		}
		r.ValueType, r.ValueInt = "int", &v
	case 9, 11: // CD: second half-word
		u := le16(val[2:4])
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
