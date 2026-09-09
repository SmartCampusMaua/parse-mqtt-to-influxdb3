// Package milesight decodes Milesight LoRaWAN sensor payloads.
//
// Reference: https://github.com/Milesight-IoT/SensorDecoders
//
// Wire format: [channel_id(1B)][type(1B)][data(N bytes)]  (repeated)
//
// Key type bytes (from official decoder.js files):
//
//	0x75  1B  uint8          battery (%)
//	0x67  2B  int16  LE ÷10  temperature (°C)
//	0x68  1B  uint8  ÷2      humidity (%)
//	0x77  2B  uint16 LE ÷100 water depth (m)
//	0x00  1B  uint8           gpio state (0=low, 1=high)
//	0xC8  4B  uint32 LE       pulse counter
//	0xE1  8B  u16+u16+f32 LE  water-conversion mode (v1.3+)
//	0x2E  1B  uint8           button press (1=short, 2=long, 3=double)
package milesight

import (
	"encoding/binary"
	"log"
	"math"
	"strings"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

// genericTypeLengths maps Milesight type bytes to data byte counts (channel-independent).
var genericTypeLengths = map[byte]int{
	0x00: 1, 0x67: 2, 0x68: 1, 0x75: 1, 0x77: 2,
	0xC8: 4, 0x2E: 1,
	// attributes (skip, no sensor_data written)
	0x01: 1, 0x08: 6, 0x09: 2, 0x0A: 2, 0x0B: 1,
	0x0F: 1, 0x10: 1, 0x16: 8, 0x1B: 5, 0xFE: 1, 0xFF: 2,
}

// channelTypeOverride maps (channel, type) pairs that differ from the generic table.
//
// NOTE: channel 0x20 type 0xCE ("history") is intentionally only registered
// once, for EM500-SWL below — EM500-SMTC (10B), VS373 (9B), AT101 (12B), and
// UC511 (9B, coincidentally the same length as VS373 but a different shape)
// all use the same (channel, type) pair with different, mutually-incompatible
// lengths. ParseMilesightTLV has no model context (it runs before the model
// is looked up), so this is not a benign omission: if any of those four
// ever sends a 0x20/0xCE TLV, it will be mis-consumed as EM500-SWL's 6
// bytes, corrupting the parse of whatever follows it in that uplink — not a
// clean "unknown channel, stop." History/backfill records are out of scope
// today (not written to sensor_data by any decoder in this package) — this
// entry cannot be safely extended to a second length without first passing
// model context into ParseMilesightTLV. Do not add another entry for that
// pair without doing so.
var channelTypeOverride = map[[2]byte]int{
	{0x05, 0xE1}: 8, // water-conv: water_conv(2B)+pulse_conv(2B)+water(4B f32 LE)
	{0x85, 0x00}: 2, // gpio alarm: gpio(1B)+alarm(1B)
	{0x85, 0xE1}: 9, // water alarm: water_conv+pulse_conv+water+alarm(1B)
	{0x20, 0xCE}: 6, // history (EM500-SWL): timestamp(4B)+depth(2B) — skipped

	// EM500-SMTC
	{0x04, 0xCA}: 2, // moisture (new resolution 0.01): uint16 LE ÷100
	{0x05, 0x7F}: 2, // electrical conductivity: uint16 LE, µS/cm direct
	{0x83, 0xD7}: 5, // temp+mutation alarm: temp(2B)+mutation(2B)+alarm_type(1B)

	// VS373
	{0x03, 0xF8}: 6, // detection target (v1.0.1): status(1B)+target(1B)+use_time_now(2B)+use_time_today(2B)
	{0x07, 0xB0}: 8, // detection target (v1.0.2): status(1B)+target(1B)+use_time_now(3B)+use_time_today(3B)
	{0x04, 0xF9}: 4, // region occupancy (v1.0.1): region1..4(1B each)
	{0x09, 0xB2}: 6, // region type (v1.0.2): region1..6(1B each) — config, skipped
	{0x0A, 0xB3}: 5, // region occupancy (v1.0.2): region_count(1B)+bitmask(4B)
	{0x05, 0xFA}: 8, // out-of-bed (v1.0.1): region1..4 time(2B each)
	{0x0B, 0xB4}: 9, // out-of-bed (v1.0.2) regions 1-3: time(3B each)
	{0x0C, 0xB4}: 9, // out-of-bed (v1.0.2) regions 4-6: time(3B each)
	{0x06, 0xFB}: 5, // alarm event: alarm_id(2B)+alarm_type(1B)+alarm_status(1B)+region_id(1B)
	{0x08, 0xB1}: 3, // breathing detection: respiratory_status(1B)+respiratory_rate(2B)

	// AT101
	{0x83, 0x67}: 3, // temperature + abnormal alarm: temp(2B)+alarm(1B)
	{0x04, 0x88}: 9, // location (normal report): lat(4B)+lon(4B)+status(1B)
	{0x84, 0x88}: 9, // location (geofence/alarm report): lat(4B)+lon(4B)+status(1B)
	{0x06, 0xD9}: 9, // wifi scan result: group(1B)+mac(6B)+rssi(1B)+motion(1B) — skipped

	// UC511/UC512 (irrigation valve + pressure controller)
	{0x09, 0x7B}: 2, // pressure: uint16 LE
	{0xB9, 0x7B}: 1, // pressure_sensor_status: uint8
	{0x21, 0xCE}: 6, // history pipe pressure: timestamp(4B)+pressure(2B) — skipped (safe: unclaimed pair, unlike 0x20/0xCE above)
}

type TLV struct {
	Channel byte
	Type    byte
	Data    []byte
}

type modelDecoder func([]TLV, string, string, time.Time) []record.SensorDataRecord

var modelDecoders = map[string]modelDecoder{
	"EM300_DI":    decodeEM300DI,
	"EM500_SWL":   decodeEM500SWL,
	"EM500_SMTC":  decodeEM500SMTC,
	"WS101":       decodeWS101,
	"WS101_SOS":   decodeWS101SOS,
	"WS101_SCENE": decodeWS101Scene,
	"VS373":       decodeVS373,
	"AT101":       decodeAT101,
	"UC511":       decodeUC511,
	"VS370":       decodeVS370,
}

// ParseMilesightTLV tokenizes a Milesight binary uplink into channel/type/data entries.
func ParseMilesightTLV(payload []byte) []TLV {
	var out []TLV
	i := 0
	for i+1 < len(payload) {
		ch, typ := payload[i], payload[i+1]
		i += 2
		l, ok := channelTypeOverride[[2]byte{ch, typ}]
		if !ok {
			l, ok = genericTypeLengths[typ]
		}
		if !ok {
			log.Printf("[milesight] unknown (ch=0x%02X type=0x%02X) — stopping", ch, typ)
			break
		}
		if i+l > len(payload) {
			log.Printf("[milesight] truncated at offset %d (need %d bytes)", i, l)
			break
		}
		out = append(out, TLV{Channel: ch, Type: typ, Data: payload[i : i+l]})
		i += l
	}
	return out
}

// Decode routes a Milesight payload to the registered model+submodel decoder.
//
// UC100/UC300 are special-cased before the generic ParseMilesightTLV call:
// their Modbus channel is genuinely variable-length (driven by an embedded
// data_type byte, not a static per-channel-type table) and needs its own
// tokenizer — see uc.go.
func Decode(deviceModel, deviceSubmodel string, payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	key := strings.ToUpper(strings.TrimSpace(deviceModel))
	if sub := strings.ToUpper(strings.TrimSpace(deviceSubmodel)); sub != "" {
		key += "_" + sub
	}

	switch key {
	case "UC100":
		return DecodeUC100(payload, deviceID, provider, ts)
	case "UC300":
		return DecodeUC300(payload, deviceID, provider, ts)
	case "UC501":
		return DecodeUC501(payload, deviceID, provider, ts)
	}

	entries := ParseMilesightTLV(payload)
	if decoder, ok := modelDecoders[key]; ok {
		return decoder(entries, deviceID, provider, ts)
	}
	log.Printf("[milesight] no decoder registered for model %s submodel %s", deviceModel, deviceSubmodel)
	return nil
}

func le16(b []byte) uint16   { return binary.LittleEndian.Uint16(b) }
func le24(b []byte) uint32   { return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 }
func le32(b []byte) uint32   { return binary.LittleEndian.Uint32(b) }
func le64(b []byte) uint64   { return binary.LittleEndian.Uint64(b) }
func f32le(b []byte) float64 { return float64(math.Float32frombits(binary.LittleEndian.Uint32(b))) }
func f64le(b []byte) float64 { return math.Float64frombits(binary.LittleEndian.Uint64(b)) }
