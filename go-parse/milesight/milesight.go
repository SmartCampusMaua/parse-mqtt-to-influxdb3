// Package milesight decodes Milesight LoRaWAN sensor payloads.
//
// Reference: https://github.com/Milesight-IoT/SensorDecoders
//
// Wire format: [channel_id(1B)][type(1B)][data(N bytes)]  (repeated)
//
// Key type bytes (from official decoder.js files):
//   0x75  1B  uint8          battery (%)
//   0x67  2B  int16  LE ÷10  temperature (°C)
//   0x68  1B  uint8  ÷2      humidity (%)
//   0x77  2B  uint16 LE ÷100 water depth (m)
//   0x00  1B  uint8           gpio state (0=low, 1=high)
//   0xC8  4B  uint32 LE       pulse counter
//   0xE1  8B  u16+u16+f32 LE  water-conversion mode (v1.3+)
//   0x2E  1B  uint8           button press (1=short, 2=long, 3=double)
package milesight

import (
	"encoding/binary"
	"log"
	"math"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/record"
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

type tlv struct{ channel, typ byte; data []byte }

func parseTLV(payload []byte) []tlv {
	var out []tlv
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
		out = append(out, tlv{ch, typ, payload[i : i+l]})
		i += l
	}
	return out
}

func le16(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }
func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
func f32le(b []byte) float64 { return float64(math.Float32frombits(binary.LittleEndian.Uint32(b))) }

// ─── EM300-DI (Pulse Counter / Digital Input) ────────────────────────────────
// Channels (from em300-di-decoder.js):
//   0x01 0x75  battery_level   (uint8 → %)
//   0x03 0x67  air_temp        (int16 LE ÷10 → °C)
//   0x04 0x68  air_rh          (uint8 ÷2 → %)
//   0x05 0x00  pulse_state     (0=low / 1=high)
//   0x05 0xC8  pulse_counter   (uint32 LE, no scaling)
//   0x05 0xE1  water_flow      (water_conv÷10 + pulse_conv÷10 + water f32 LE → m³)
//   0x85 0x00  gpio alarm      (skipped – alarm meta, not a sensor measurement)
func DecodeEM300DI(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "em300_di"
	st := record.ST
	var out []record.SensorDataRecord
	for _, e := range parseTLV(payload) {
		switch {
		case e.channel == 0x01 && e.typ == 0x75:
			out = append(out, record.NewFloat(st.BatteryLevel, dm, deviceID, provider, float64(e.data[0]), ts))
		case e.channel == 0x03 && e.typ == 0x67:
			out = append(out, record.NewFloat(st.AirTemp, dm, deviceID, provider, float64(int16(le16(e.data)))/10.0, ts))
		case e.channel == 0x04 && e.typ == 0x68:
			out = append(out, record.NewFloat(st.AirRH, dm, deviceID, provider, float64(e.data[0])/2.0, ts))
		case e.channel == 0x05 && e.typ == 0x00:
			out = append(out, record.NewBool(st.PulseState, dm, deviceID, provider, e.data[0] == 0x01, ts))
		case e.channel == 0x05 && e.typ == 0xC8:
			// uint32 LE — direct count, no scaling
			out = append(out, record.NewInt(st.PulseCounter, dm, deviceID, provider, int64(le32(e.data)), ts))
		case e.channel == 0x05 && e.typ == 0xE1:
			// Water-conversion mode (v1.3+): water_conv(2B÷10) + pulse_conv(2B÷10) + water(f32 LE m³)
			// water_conv and pulse_conv are calibration coefficients; only the final water volume is stored.
			water := f32le(e.data[4:8])
			out = append(out, record.NewFloat(st.WaterFlow, dm, deviceID, provider, water, ts))
		// 0x85 0x00 gpio alarm and 0x85 0xE1 water alarm: alarm meta, not written to sensor_data
		}
	}
	return out
}

// ─── EM500-SWL (Submersible Water Level) ─────────────────────────────────────
// Channels (from em500-swl-decoder.js):
//   0x01 0x75  battery_level   (uint8 → %)
//   0x03 0x77  water_level     (uint16 LE ÷100 → m;  0xFFFF=collection failed, 0xFFFD=out of range)
func DecodeEM500SWL(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "em500_swl"
	st := record.ST
	var out []record.SensorDataRecord
	for _, e := range parseTLV(payload) {
		switch {
		case e.channel == 0x01 && e.typ == 0x75:
			out = append(out, record.NewFloat(st.BatteryLevel, dm, deviceID, provider, float64(e.data[0]), ts))
		case e.channel == 0x03 && e.typ == 0x77:
			raw := le16(e.data)
			switch raw {
			case 0xFFFF:
				log.Printf("[milesight] EM500-SWL[%s]: depth sensor collection failed", deviceID)
			case 0xFFFD:
				log.Printf("[milesight] EM500-SWL[%s]: depth sensor out of range", deviceID)
			default:
				// uint16 LE ÷ 100 → metres  (official decoder.js: depth_value / 100)
				out = append(out, record.NewFloat(st.WaterLevel, dm, deviceID, provider, float64(raw)/100.0, ts))
			}
		}
	}
	return out
}

// ─── WS101 (Smart Button) ─────────────────────────────────────────────────────
// All WS101 submodels (including WS101-R colour variant) share the same payload.
// Device model key: "ws101"
// Channels (from ws101-decoder.js):
//   0x01 0x75  battery_level   (uint8 → %)
//   0xFF 0x2E  press_type      (1=short, 2=long, 3=double)
func DecodeWS101(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "ws101"
	st := record.ST
	var out []record.SensorDataRecord
	for _, e := range parseTLV(payload) {
		switch {
		case e.channel == 0x01 && e.typ == 0x75:
			out = append(out, record.NewFloat(st.BatteryLevel, dm, deviceID, provider, float64(e.data[0]), ts))
		case e.channel == 0xFF && e.typ == 0x2E:
			out = append(out, record.NewInt(st.PressType, dm, deviceID, provider, int64(e.data[0]), ts))
		}
	}
	return out
}
