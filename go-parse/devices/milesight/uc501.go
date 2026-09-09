// Reference: https://github.com/Milesight-IoT/SensorDecoders/blob/main/uc-series/uc501/uc501-decoder.js
//
// UC501/UC50x is Modbus + site-wired GPIO/analog-input, the same kind of
// device as UC100/UC300 — but the wire format genuinely differs: its Modbus
// entry has a 2-byte header (not 3) with no sign bit, its GPIO/ADC channel
// numbering doesn't overlap with UC300's, and its ADC channel has two
// firmware-version-dependent formats (v2: int16/1000; v3: float16). See the
// ParseUCTLV/uc.go doc comment for why the Modbus shape needed
// parameterizing rather than reusing UC100/UC300's directly.
//
// battery is UC501's only fixed/intrinsic reading; everything else (GPIO,
// analog input, Modbus) is site-configurable, so — like UC300's GPIO/PT100/
// ADC — those carry IOChannel/ModbusChannel instead of SensorType, resolved
// via device_channels.json + sensors.json, never a name invented here.
package milesight

import (
	"log"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

const dmUC501 = "uc501"

// uc501ChannelLengths: byte lengths only, sourced directly from the official
// decoder.js gpio_chns/adc_chns/adc_alarm_chns tables plus the fixed-length
// SDI-12 channel. No semantic meaning is assigned to any of these entries.
var uc501ChannelLengths = map[[2]byte]int{
	{0x03, 0x00}: 1, {0x04, 0x00}: 1, // gpio input on/off
	{0x03, 0x01}: 1, {0x04, 0x01}: 1, // gpio output on/off
	{0x03, 0xC8}: 4, {0x04, 0xC8}: 4, // gpio as pulse counter
	{0x05, 0x02}: 8, {0x06, 0x02}: 8, // adc v2 (firmware <=1.10 / UC50x V1): value+min+max+avg int16 LE /1000
	{0x05, 0xE2}: 8, {0x06, 0xE2}: 8, // adc v3: value+min+max+avg float16
	{0x85, 0xE2}: 9, {0x86, 0xE2}: 9, // adc v3 alarm variant: value+min+max+avg float16 + alarm(1B)
	{0x08, 0xDB}: 37, // SDI-12: probe_index(1B)+ascii_string(36B) — tokenized only, not decoded (string payload doesn't fit float/int/bool)
}

// isUC501ModbusEntry / uc501ModbusEntryLen implement UC501's Modbus entry
// shape for ParseUCTLV: [modbus_chn_id(1B)][package_type(1B)][value...], no
// separate data_length byte (unlike UC100/UC300), data_type is package_type
// & 0x07 (3 bits, no sign bit), and channel_id=0x80 (vs the normal 0xFF)
// carries one extra trailing alarm byte after the value.
func isUC501ModbusEntry(ch, typ byte) bool {
	return (ch == 0xFF || ch == 0x80) && typ == 0x0E
}

func uc501ModbusEntryLen(ch byte, payload []byte, offset int) (int, bool) {
	if offset+2 > len(payload) {
		return 0, false
	}
	var n int
	switch payload[offset+1] & 0x07 {
	case 0, 1:
		n = 1
	case 2, 3:
		n = 2
	case 4, 5, 6, 7:
		n = 4
	default:
		return 0, false
	}
	total := 2 + n
	if ch == 0x80 {
		total++ // trailing alarm byte
	}
	return total, true
}

// DecodeUC501 decodes UC501/UC50x binary payloads from LoRaWAN uplinks.
func DecodeUC501(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	entries := ParseUCTLV(payload, uc501ChannelLengths, isUC501ModbusEntry, uc501ModbusEntryLen)
	return decodeUC501(entries, deviceID, provider, ts)
}

// decodeUC501 extracts battery, GPIO/analog-input, and Modbus channel
// readings from a UC501 uplink. Only battery carries a fixed sensor_type —
// everything else carries IOChannel or ModbusChannel instead, resolved
// post-decode by main.go, dropped if unconfigured rather than written under
// a made-up name.
func decodeUC501(entries []UCEntry, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = dmUC501
	st := record.ST
	var out []record.SensorDataRecord

	for _, e := range entries {
		switch {
		case e.ChannelID == 0x01 && e.ChannelType == 0x75:
			out = append(out, record.NewFloat(st.BatteryLevel, dm, deviceID, provider, float64(e.Data[0]), ts))

		case (e.ChannelID == 0x03 || e.ChannelID == 0x04) && (e.ChannelType == 0x00 || e.ChannelType == 0x01):
			// GPIO input (0x00) or output (0x01) on/off — same channel_id
			// space, disambiguated at config time by which variant actually
			// arrives (a pin is wired as one or the other, not both).
			r := record.SensorDataRecord{DeviceModel: dm, DeviceID: deviceID, Provider: provider, Timestamp: ts, IOChannel: int(e.ChannelID)}
			v := e.Data[0] != 0
			r.ValueType, r.ValueBool = "bool", &v
			out = append(out, r)

		case (e.ChannelID == 0x03 || e.ChannelID == 0x04) && e.ChannelType == 0xC8:
			r := record.SensorDataRecord{DeviceModel: dm, DeviceID: deviceID, Provider: provider, Timestamp: ts, IOChannel: int(e.ChannelID)}
			v := int64(le32(e.Data))
			r.ValueType, r.ValueInt = "int", &v
			out = append(out, r)

		case (e.ChannelID == 0x05 || e.ChannelID == 0x06) && e.ChannelType == 0x02:
			r := record.SensorDataRecord{DeviceModel: dm, DeviceID: deviceID, Provider: provider, Timestamp: ts, IOChannel: int(e.ChannelID)}
			v := float64(int16(le16(e.Data[0:2]))) / 1000
			r.ValueType, r.ValueFloat = "float", &v
			out = append(out, r)

		case (e.ChannelID == 0x05 || e.ChannelID == 0x06) && e.ChannelType == 0xE2:
			r := record.SensorDataRecord{DeviceModel: dm, DeviceID: deviceID, Provider: provider, Timestamp: ts, IOChannel: int(e.ChannelID)}
			v := f16le(e.Data[0:2])
			r.ValueType, r.ValueFloat = "float", &v
			out = append(out, r)

		case (e.ChannelID == 0x85 || e.ChannelID == 0x86) && e.ChannelType == 0xE2:
			// Alarm-variant report of the same logical analog input as the
			// 0x05/0x06 channel above — remapped to the same IOChannel so it
			// resolves to the same sensor_id regardless of which variant
			// triggered this particular uplink.
			r := record.SensorDataRecord{DeviceModel: dm, DeviceID: deviceID, Provider: provider, Timestamp: ts, IOChannel: int(e.ChannelID) - 0x80}
			v := f16le(e.Data[0:2])
			r.ValueType, r.ValueFloat = "float", &v
			out = append(out, r)

		case isUC501ModbusEntry(e.ChannelID, e.ChannelType):
			r, ok := decodeUC501ModbusValue(e.Data, deviceID, provider, ts)
			if !ok {
				continue
			}
			out = append(out, r)

		case e.ChannelID == 0xFF && e.ChannelType == 0x15:
			chn := int(e.Data[0]) - 6
			log.Printf("[uc501] device_id=%s modbus_channel=%d read error reported by device", deviceID, chn)
		}
	}

	return out
}

// decodeUC501ModbusValue decodes one MODBUS channel entry's value bytes.
// data is [modbus_chn_id][package_type][value...] (+ 1 trailing alarm byte,
// ignored via bounded slicing below, on the 0x80 channel variant), as
// tokenized by ParseUCTLV. Ported byte-for-byte from the official decoder's
// MODBUS switch — no sign bit, no AB/CD half-word split (unlike UC100/UC300).
func decodeUC501ModbusValue(data []byte, deviceID, provider string, ts time.Time) (record.SensorDataRecord, bool) {
	chn := int(data[0]) - 6
	dataType := data[1] & 0x07
	val := data[2:]

	r := record.SensorDataRecord{
		DeviceModel:   dmUC501,
		DeviceID:      deviceID,
		Provider:      provider,
		Timestamp:     ts,
		ModbusChannel: chn,
	}

	switch dataType {
	case 0, 1:
		v := val[0] != 0
		r.ValueType, r.ValueBool = "bool", &v
	case 2, 3:
		v := int64(le16(val[:2]))
		r.ValueType, r.ValueInt = "int", &v
	case 4, 6:
		v := int64(le32(val[:4]))
		r.ValueType, r.ValueInt = "int", &v
	case 5, 7:
		v := f32le(val[:4])
		r.ValueType, r.ValueFloat = "float", &v
	default:
		return record.SensorDataRecord{}, false
	}

	return r, true
}
