package khomp

import (
	"log"
	"math"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/internal/record"
)

// DTL200 fixed hardware parameters.
// The DTL200 uses 3×AA lithium primary cells and a 0–5 m default probe range.
const (
	batteryVoltageMax = 3.6 // V, full charge — Khomp TPBT AA
	batteryVoltageMin = 3.0 // V, cutoff
	probeRangeM       = 5.0 // metres, default full-scale probe range
)

func decodeDTL200(payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
	if port == 5 || port == 7 {
		return nil
	}
	if len(payload) < 9 {
		log.Printf("[khomp] decodeDTL200[%s]: short payload (%d bytes, need >=9)", deviceID, len(payload))
		return nil
	}

	const dm = "dtl200"
	st := record.ST
	u16be := func(i int) float64 { return float64(uint16(payload[i])<<8 | uint16(payload[i+1])) }

	batV := u16be(0) / 1000.0
	probeMode := payload[2]
	rangeKey := payload[3]
	idcMa := u16be(4) / 1000.0
	vdcV := u16be(6) / 1000.0

	effectiveRange := float64(rangeKey)
	if rangeKey == 0 {
		effectiveRange = probeRangeM
	}

	batLevel := math.Max(0, math.Min(100, (batV-batteryVoltageMin)/(batteryVoltageMax-batteryVoltageMin)*100.0))

	var wl float64
	switch probeMode {
	case 0x00:
		if idcMa > 4.0 {
			wl = math.Round((idcMa-4.0)*(effectiveRange/16.0)*100) / 100
		}
	case 0x01:
		if idcMa > 4.0 {
			mpaSF := map[byte]float64{1: 0.0375, 2: 0.0625, 3: 0.1, 4: 0.15625, 5: 0.625, 6: 2.5, 7: 3.75, 8: -0.00625}
			if sf, ok := mpaSF[rangeKey]; ok {
				wl = (idcMa - 4.0) * sf
			} else if rangeKey == 9 {
				if idcMa <= 12.0 {
					wl = (idcMa - 4.0) * -0.0125
				} else {
					wl = (idcMa - 12.0) * 0.0125
				}
			} else {
				kPaSF := map[byte]float64{10: 0.3125, 11: 3.125, 12: 6.25}
				if sf, ok := kPaSF[rangeKey]; ok {
					wl = (idcMa - 4.0) * sf / 1000.0
				}
			}
		}
	case 0x02:
		paSF := map[byte]float64{1: 6.25, 2: 12.5, 3: 18.75, 4: 62.5, 5: 125, 6: 187.5, 7: 250, 8: 312.5, 9: 625}
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
	}

	out := []record.SensorDataRecord{
		record.NewFloat(st.BatteryLevel, dm, deviceID, provider, batLevel, ts),
		record.NewFloat(st.CurrentLoop, dm, deviceID, provider, idcMa, ts),
		record.NewFloat(st.VoltageInput, dm, deviceID, provider, vdcV, ts),
	}
	if probeMode == 0x00 || probeMode == 0x01 || probeMode == 0x02 {
		out = append(out, record.NewFloat(st.WaterLevel, dm, deviceID, provider, wl, ts))
	}
	return out
}

// DecodeDTL200 decodes DTL200 binary payloads (4-20 mA / 0-30 V converter).
func DecodeDTL200(payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
	return decodeDTL200(payload, deviceID, provider, port, ts)
}
