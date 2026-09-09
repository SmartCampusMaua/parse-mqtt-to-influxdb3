// Reference: https://github.com/Milesight-IoT/SensorDecoders/blob/main/uc-series/uc511/uc511-decoder.js
//
// UC511/UC512 is a fixed-purpose irrigation valve + pipe-pressure
// controller — unlike UC100/UC300/UC501, it has no RS485/Modbus port at all
// (confirmed: no 0x19/0x0E channel anywhere in the official decoder), so it
// uses the standard static-length ParseMilesightTLV, not uc.go's variable-
// length walker. Every field below has a fixed, device-intrinsic meaning
// (battery, two valves, pipe pressure), unlike UC300's GPIO/PT100/ADC —
// nothing here is site-configurable, so no channel-routing is needed either.
package milesight

import (
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

const dmUC511 = "uc511"

var (
	gpioChnsUC511  = []byte{0x07, 0x08}
	valveChnsUC511 = []byte{0x03, 0x05}
	pulseChnsUC511 = []byte{0x04, 0x06}
)

// decodeUC511 decodes UC511/UC512 fixed-purpose channels. Downlink command
// echoes (valve delay-control results, LoRaWAN class switch responses,
// schedule/multicast/AI-collection-config responses), custom_message
// (variable-length ASCII, no safe fixed length to register), and history
// channels (0x20/0xCE — see the cross-device collision note in
// channelTypeOverride) are not decoded into sensor_data records.
func decodeUC511(entries []TLV, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	const dm = dmUC511
	st := record.ST
	var out []record.SensorDataRecord

	for _, e := range entries {
		switch {
		case e.Channel == 0x01 && e.Type == 0x75:
			out = append(out, record.NewFloat(st.BatteryLevel, dm, deviceID, provider, float64(e.Data[0]), ts))

		case includes(valveChnsUC511, e.Channel) && e.Type == 0x01:
			raw := e.Data[0]
			if raw == 0xFF {
				// Delay-control command result echo, not a status reading — skip.
				continue
			}
			deviceIndex := 1
			if e.Channel == valveChnsUC511[1] {
				deviceIndex = 2
			}
			r := record.NewBool(st.SolenoidValveStatus, dm, deviceID, provider, raw == 1, ts)
			r.DeviceIndex = deviceIndex
			out = append(out, r)

		case includes(pulseChnsUC511, e.Channel) && e.Type == 0xC8:
			deviceIndex := 1
			if e.Channel == pulseChnsUC511[1] {
				deviceIndex = 2
			}
			r := record.NewInt(st.PulseCount, dm, deviceID, provider, int64(le32(e.Data)), ts)
			r.DeviceIndex = deviceIndex
			out = append(out, r)

		case includes(gpioChnsUC511, e.Channel) && e.Type == 0x01:
			deviceIndex := 1
			if e.Channel == gpioChnsUC511[1] {
				deviceIndex = 2
			}
			r := record.NewBool(st.DigitalInput, dm, deviceID, provider, e.Data[0] == 1, ts)
			r.DeviceIndex = deviceIndex
			out = append(out, r)

		case e.Channel == 0x09 && e.Type == 0x7B:
			out = append(out, record.NewInt(st.WaterPressure, dm, deviceID, provider, int64(le16(e.Data)), ts))

		case e.Channel == 0xB9 && e.Type == 0x7B:
			out = append(out, record.NewBool(st.PressureSensorFailStatus, dm, deviceID, provider, e.Data[0] == 1, ts))
		}
	}

	return out
}

// DecodeUC511 decodes UC511/UC512 binary payloads from LoRaWAN uplinks.
func DecodeUC511(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeUC511(ParseMilesightTLV(payload), deviceID, provider, ts)
}

func includes(chns []byte, ch byte) bool {
	for _, c := range chns {
		if c == ch {
			return true
		}
	}
	return false
}
