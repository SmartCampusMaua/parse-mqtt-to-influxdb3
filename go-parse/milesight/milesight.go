// Package milesight decodes Milesight LoRaWAN sensor payloads.
// Uses the generic channel-type-value (AM19Hex) protocol described in the
// MilesightParser reference.
//
// Wire format:  [channelID][channelType][value_bytes…]  (repeated)
// channelID → semantics (hex):
//   0x01 battery  0x02 temperature  0x03 humidity  0x04 co2  0x0C motion
//   0x0D occupancy  0x0E people_count  0x0F door_state
// channelType → width:
//   0x00 BOOL 1B  0x01 INT8 1B  0x02 UINT8 1B  0x03 INT16 2B  0x04 UINT16 2B
//   0x05 INT32 4B  0x06 UINT32 4B  0x07 FLOAT32 4B (IEEE-754 big-endian)
package milesight

import (
	"encoding/binary"
	"math"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/record"
)

// parseAM19HexChannels decodes all TLV triplets in a Milesight payload into
// a slice of record.AM19HexChannel.  Unknown channel types are silently skipped.
func parseAM19HexChannels(payload []byte) []record.AM19HexChannel {
	var out []record.AM19HexChannel
	i := 0
	for i+1 < len(payload) {
		chID := payload[i]
		chType := payload[i+1]
		i += 2
		ch := record.AM19HexChannel{ID: chID}
		switch chType {
		case 0x00:
			if i >= len(payload) {
				return out
			}
			ch.IsBool, ch.BoolVal = true, payload[i] != 0
			i++
		case 0x01:
			if i >= len(payload) {
				return out
			}
			ch.Value = float64(int8(payload[i]))
			i++
		case 0x02:
			if i >= len(payload) {
				return out
			}
			ch.Value = float64(payload[i])
			i++
		case 0x03:
			if i+1 >= len(payload) {
				return out
			}
			ch.Value = float64(int16(binary.BigEndian.Uint16(payload[i : i+2])))
			i += 2
		case 0x04:
			if i+1 >= len(payload) {
				return out
			}
			ch.Value = float64(binary.BigEndian.Uint16(payload[i : i+2]))
			i += 2
		case 0x05:
			if i+3 >= len(payload) {
				return out
			}
			ch.Value = float64(int32(binary.BigEndian.Uint32(payload[i : i+4])))
			i += 4
		case 0x06:
			if i+3 >= len(payload) {
				return out
			}
			ch.Value = float64(binary.BigEndian.Uint32(payload[i : i+4]))
			i += 4
		case 0x07:
			if i+3 >= len(payload) {
				return out
			}
			ch.Value = float64(math.Float32frombits(binary.BigEndian.Uint32(payload[i : i+4])))
			i += 4
		default:
			continue
		}
		out = append(out, ch)
	}
	return out
}

// DecodeEM300DI decodes Milesight EM300-DI (Digital Input / Pulse Counter).
// Channel → sensor_type mapping (record.ST):
//   CH01 UINT8  battery_level       (%)
//   CH02 INT16  air_temp            (raw÷10 → °C)
//   CH03 UINT8  air_rh              (raw÷2  → %RH)
//   CH0C BOOL   pulse_state         (GPIO digital input state)
//   CH0E UINT32 pulse_counter       (cumulative)
func DecodeEM300DI(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "em300_di"
	st := record.ST
	var out []record.SensorDataRecord
	for _, ch := range parseAM19HexChannels(payload) {
		switch {
		case ch.ID == 0x01 && !ch.IsBool:
			out = append(out, record.NewFloat(st.BatteryLevel, dm, deviceID, provider, ch.Value, ts))
		case ch.ID == 0x02 && !ch.IsBool:
			out = append(out, record.NewFloat(st.AirTemp, dm, deviceID, provider, ch.Value/10.0, ts))
		case ch.ID == 0x03 && !ch.IsBool:
			out = append(out, record.NewFloat(st.AirRH, dm, deviceID, provider, ch.Value/2.0, ts))
		case ch.ID == 0x0C && ch.IsBool:
			out = append(out, record.NewBool(st.PulseState, dm, deviceID, provider, ch.BoolVal, ts))
		case ch.ID == 0x0E && !ch.IsBool:
			out = append(out, record.NewInt(st.PulseCounter, dm, deviceID, provider, int64(ch.Value), ts))
		}
	}
	return out
}

// DecodeEM500SWL decodes Milesight EM500-SWL (Soil / Water Level Sensor).
// Channel → sensor_type mapping (record.ST):
//   CH01 UINT8  battery_level          (%)
//   CH02 INT16  air_temp               (raw÷10 → °C, soil probe temp)
//   CH03 UINT16 water_level            (raw÷10 → % volumetric water content)
//   CH04 UINT16 electrical_conductivity (µS/cm)
func DecodeEM500SWL(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "em500_swl"
	st := record.ST
	var out []record.SensorDataRecord
	for _, ch := range parseAM19HexChannels(payload) {
		switch {
		case ch.ID == 0x01 && !ch.IsBool:
			out = append(out, record.NewFloat(st.BatteryLevel, dm, deviceID, provider, ch.Value, ts))
		case ch.ID == 0x02 && !ch.IsBool:
			out = append(out, record.NewFloat(st.AirTemp, dm, deviceID, provider, ch.Value/10.0, ts))
		case ch.ID == 0x03 && !ch.IsBool:
			out = append(out, record.NewFloat(st.WaterLevel, dm, deviceID, provider, ch.Value/10.0, ts))
		case ch.ID == 0x04 && !ch.IsBool:
			out = append(out, record.NewFloat(st.ElectricalConductivity, dm, deviceID, provider, ch.Value, ts))
		}
	}
	return out
}

// DecodeWS101R decodes Milesight WS101-R (LoRaWAN Smart Button).
// Channel → sensor_type mapping (record.ST):
//   CH01 UINT8  battery_level  (%)
//   CH0C UINT8  press_type     (1=single, 2=long, 3=double)
//   CH0C BOOL   press_state    (immediate event)
//   CH0E UINT32 press_count    (cumulative)
func DecodeWS101R(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = "ws101_r"
	st := record.ST
	var out []record.SensorDataRecord
	for _, ch := range parseAM19HexChannels(payload) {
		switch {
		case ch.ID == 0x01 && !ch.IsBool:
			out = append(out, record.NewFloat(st.BatteryLevel, dm, deviceID, provider, ch.Value, ts))
		case ch.ID == 0x0C && !ch.IsBool:
			out = append(out, record.NewInt(st.PressType, dm, deviceID, provider, int64(ch.Value), ts))
		case ch.ID == 0x0C && ch.IsBool:
			out = append(out, record.NewBool(st.PressState, dm, deviceID, provider, ch.BoolVal, ts))
		case ch.ID == 0x0E && !ch.IsBool:
			out = append(out, record.NewInt(st.PressCount, dm, deviceID, provider, int64(ch.Value), ts))
		}
	}
	return out
}
