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
var channelTypeOverride = map[[2]byte]int{
	{0x05, 0xE1}: 8, // water-conv: water_conv(2B)+pulse_conv(2B)+water(4B f32 LE)
	{0x85, 0x00}: 2, // gpio alarm: gpio(1B)+alarm(1B)
	{0x85, 0xE1}: 9, // water alarm: water_conv+pulse_conv+water+alarm(1B)
	{0x20, 0xCE}: 6, // history (EM500-SWL): timestamp(4B)+depth(2B) — skipped
}

type TLV struct {
	Channel byte
	Type    byte
	Data    []byte
}

type modelDecoder func([]TLV, string, string, time.Time) []record.SensorDataRecord

var modelDecoders = map[string]modelDecoder{
	"EM300_DI":  decodeEM300DI,
	"EM500_SWL": decodeEM500SWL,
	"WS101":     decodeWS101,
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
func Decode(deviceModel, deviceSubmodel string, payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	entries := ParseMilesightTLV(payload)
	key := strings.ToUpper(strings.TrimSpace(deviceModel))
	if sub := strings.ToUpper(strings.TrimSpace(deviceSubmodel)); sub != "" {
		key += "_" + sub
	}
	if decoder, ok := modelDecoders[key]; ok {
		return decoder(entries, deviceID, provider, ts)
	}
	log.Printf("[milesight] no decoder registered for model %s submodel %s", deviceModel, deviceSubmodel)
	return nil
}

func le16(b []byte) uint16   { return binary.LittleEndian.Uint16(b) }
func le32(b []byte) uint32   { return binary.LittleEndian.Uint32(b) }
func f32le(b []byte) float64 { return float64(math.Float32frombits(binary.LittleEndian.Uint32(b))) }
