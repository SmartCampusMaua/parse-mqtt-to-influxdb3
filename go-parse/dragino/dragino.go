// Package dragino decodes Dragino LoRaWAN sensor payloads.
package dragino

import (
	"log"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/record"
)

// DecodeDTL200SWL decodes Dragino DTL200-SWL (4-20 mA Liquid Level Transmitter).
//
// The decoder dispatches on fPort (extracted from the LNS frame):
//   fPort 5  → configuration/attribute frame  — skipped
//   fPort 7  → datalog frame                  — skipped
//   else     → standard uplink (translated from Dragino JS reference decoder)
//
// Standard uplink wire format:
//   bytes[0:2]  battery_voltage  (÷1000 → V)
//   bytes[2]    probe_mode       0x00=depth  0x01=pressure(MPa)  0x02=diff.press(Pa)
//   bytes[3]    range key        probe range in metres (0x01–0xFF); 0 = not configured
//   bytes[4:6]  idc_input_ma     (÷1000 → mA)
//   bytes[6:8]  vdc_input_v      (÷1000 → V)
//   bytes[8]    flags            bit3=IN1_high  bit2=IN2_high  bit1=Exti_high  bit0=Exti_status
//
// ── Why water_level may be zero ──────────────────────────────────────────────
// probe_mode 0x00: water_level = (IDC_mA − 4.0) × (range_key × 100 / 16)
//   Zero when:  a) IDC ≤ 4.0 mA  (sensor at/below minimum → no water)
//               b) bytes[3] = 0  (probe range not configured in device settings)
// Use the raw idc_input_ma value written alongside to distinguish these cases.
func DecodeDTL200SWL(payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
	// Skip non-sensor frames
	if port == 5 {
		log.Printf("[dragino] DTL200-SWL[%s]: skipping fPort 5 (config frame)", deviceID)
		return nil
	}
	if port == 7 {
		log.Printf("[dragino] DTL200-SWL[%s]: skipping fPort 7 (datalog frame)", deviceID)
		return nil
	}
	if len(payload) < 9 {
		log.Printf("[dragino] DTL200-SWL[%s]: short payload (%d bytes, need ≥9)", deviceID, len(payload))
		return nil
	}

	const dm = "dtl200_swl"
	st := record.ST

	// big-endian uint16 from two consecutive bytes
	u16be := func(i int) float64 { return float64(uint16(payload[i])<<8 | uint16(payload[i+1])) }

	batV      := u16be(0) / 1000.0 // (bytes[0]<<8 | bytes[1]) / 1000
	probeMode := payload[2]
	rangeKey  := payload[3]         // probe range in metres; 0 = not configured
	idcMa     := u16be(4) / 1000.0 // (bytes[4]<<8 | bytes[5]) / 1000
	vdcV      := u16be(6) / 1000.0 // (bytes[6]<<8 | bytes[7]) / 1000
	flags     := payload[8]

	out := []record.SensorDataRecord{
		record.NewFloat(st.BatteryVoltage, dm, deviceID, provider, batV,  ts),
		record.NewFloat(st.IdcInputMA,     dm, deviceID, provider, idcMa, ts),
		record.NewFloat(st.VdcInputV,      dm, deviceID, provider, vdcV,  ts),
		record.NewBool(st.IN1PinHigh,  dm, deviceID, provider, flags&0x08 != 0, ts),
		record.NewBool(st.IN2PinHigh,  dm, deviceID, provider, flags&0x04 != 0, ts),
		record.NewBool(st.ExtiStatus,  dm, deviceID, provider, flags&0x01 != 0, ts),
	}

	if rangeKey == 0 {
		log.Printf("[dragino] DTL200-SWL[%s]: bytes[3] (probe range) = 0 — water_level will be 0; check device configuration", deviceID)
	}

	var wl float64
	switch probeMode {

	case 0x00: // Water depth → cm
		// Formula: Water_deep_cm = (IDC_mA − 4.0) × (rangeKey × 100 / 16)
		if idcMa > 4.0 {
			wl = (idcMa - 4.0) * (float64(rangeKey) * 100.0 / 16.0)
		}
		out = append(out, record.NewFloat(st.WaterLevel, dm, deviceID, provider, wl, ts))

	case 0x01: // Water pressure → MPa
		if idcMa > 4.0 {
			mpaSF := map[byte]float64{
				1: 0.0375, 2: 0.0625, 3: 0.1, 4: 0.15625,
				5: 0.625, 6: 2.5, 7: 3.75, 8: -0.00625,
			}
			if sf, ok := mpaSF[rangeKey]; ok {
				wl = (idcMa - 4.0) * sf
			} else if rangeKey == 9 {
				if idcMa <= 12.0 {
					wl = (idcMa - 4.0) * -0.0125
				} else {
					wl = (idcMa - 12.0) * 0.0125
				}
			}
		}
		// kPa variants (rangeKey 10-12)
		kPaSF := map[byte]float64{10: 0.3125, 11: 3.125, 12: 6.25}
		if sf, ok := kPaSF[rangeKey]; ok && idcMa > 4.0 {
			wl = (idcMa - 4.0) * sf / 1000.0 // kPa → MPa for consistent unit
		}
		out = append(out, record.NewFloat(st.WaterLevel, dm, deviceID, provider, wl, ts))

	case 0x02: // Differential pressure → Pa
		paSF := map[byte]float64{
			1: 6.25, 2: 12.5, 3: 18.75, 4: 62.5,
			5: 125, 6: 187.5, 7: 250, 8: 312.5, 9: 625,
		}
		bidiSF := map[byte]float64{10: 12.5, 11: 25, 12: 125}
		if sf, ok := paSF[rangeKey]; ok && idcMa > 4.0 {
			wl = (idcMa - 4.0) * sf
		} else if sf, ok := bidiSF[rangeKey]; ok {
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
