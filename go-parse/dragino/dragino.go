// Package dragino decodes Dragino LoRaWAN sensor payloads.
package dragino

import (
	"log"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/record"
)

// DecodeDTL200SWL decodes Dragino DTL200-SWL (Liquid Level Sensor) standard uplinks.
// Wire format (from Dragino JavaScript reference decoder):
//   bytes[0:2]  battery_voltage  (÷1000 → V)
//   bytes[2]    probe_mode       0x00=depth(cm)  0x01=pressure(MPa)  0x02=diff.press(Pa)
//   bytes[3]    scale key        selects scaling formula
//   bytes[4:6]  idc_input_ma     (÷1000 → mA)
//   bytes[6:8]  vdc_input_v      (÷1000 → V)
//   bytes[8]    flags            bit3=IN1_high bit2=IN2_high bit1=Exti_high bit0=Exti_status
//
// The primary measurement water_level unit depends on probe mode:
//   0x00 → cm    0x01 → MPa    0x02 → Pa
func DecodeDTL200SWL(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	if len(payload) < 9 {
		log.Printf("[dragino] DecodeDTL200SWL[%s]: short payload (%d bytes, need ≥9)", deviceID, len(payload))
		return nil
	}
	const dm = "dtl200_swl"
	st := record.ST
	u16 := func(i int) float64 { return float64(uint16(payload[i])<<8 | uint16(payload[i+1])) }

	batV      := u16(0) / 1000.0
	probeMode := payload[2]
	scaleKey  := payload[3]
	idcMa     := u16(4) / 1000.0
	vdcV      := u16(6) / 1000.0
	flags     := payload[8]

	out := []record.SensorDataRecord{
		record.NewFloat(st.BatteryVoltage, dm, deviceID, provider, batV,  ts),
		record.NewFloat(st.IdcInputMA,     dm, deviceID, provider, idcMa, ts),
		record.NewFloat(st.VdcInputV,      dm, deviceID, provider, vdcV,  ts),
		record.NewBool(st.IN1PinHigh, dm, deviceID, provider, flags&0x08 != 0, ts),
		record.NewBool(st.IN2PinHigh, dm, deviceID, provider, flags&0x04 != 0, ts),
		record.NewBool(st.ExtiStatus,  dm, deviceID, provider, flags&0x01 != 0, ts),
	}

	var wl float64
	switch probeMode {
	case 0x00: // depth (cm)
		if idcMa > 4.0 {
			wl = (idcMa - 4.0) * (float64(scaleKey) * 100.0 / 16.0)
		}
		out = append(out, record.NewFloat(st.WaterLevel, dm, deviceID, provider, wl, ts))

	case 0x01: // pressure (MPa)
		mpaSF := map[byte]float64{1: 0.0375, 2: 0.0625, 3: 0.1, 4: 0.15625, 5: 0.625, 6: 2.5, 7: 3.75}
		if sf, ok := mpaSF[scaleKey]; ok && idcMa > 4.0 {
			wl = (idcMa - 4.0) * sf
		} else if scaleKey == 9 {
			if idcMa <= 12.0 {
				wl = (idcMa - 4.0) * -0.0125
			} else {
				wl = (idcMa - 12.0) * 0.0125
			}
		}
		out = append(out, record.NewFloat(st.WaterLevel, dm, deviceID, provider, wl, ts))

	case 0x02: // differential pressure (Pa)
		paSF   := map[byte]float64{1: 6.25, 2: 12.5, 3: 18.75, 4: 62.5, 5: 125, 6: 187.5, 7: 250, 8: 312.5, 9: 625}
		bidiSF := map[byte]float64{10: 12.5, 11: 25, 12: 125}
		if sf, ok := paSF[scaleKey]; ok && idcMa > 4.0 {
			wl = (idcMa - 4.0) * sf
		} else if sf, ok := bidiSF[scaleKey]; ok {
			if idcMa <= 12.0 {
				wl = (idcMa - 4.0) * (-sf)
			} else {
				wl = (idcMa - 12.0) * sf
			}
		}
		out = append(out, record.NewFloat(st.WaterLevel, dm, deviceID, provider, wl, ts))
	}
	return out
}
