// Package imt decodes IMT LoRaWAN sensor payloads (soil moisture depth
// probes, solenoid valve controllers).
//
// Wire format: a flat sequence of tag bytes, each followed by a
// tag-specific number of data bytes — there is no explicit length prefix,
// the parser knows how many bytes each tag consumes. Some tags (0x01, 0x03,
// 0x0D, 0x0E) repeat and fan out into indexed struct fields in the order
// they appear. An unrecognized tag byte terminates parsing.
package imt

import (
	"log"
	"strings"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

// IMT holds every field the IMT tag protocol can produce across all device
// variants. Each device decoder reads only the fields relevant to it.
type IMT struct {
	X_01_0 float64
	X_01_1 float64
	X_01_2 float64
	X_01_3 float64
	X_01_4 float64
	X_01_5 float64
	X_01_6 float64
	X_01_7 float64
	X_02   float64
	X_03_0 float64
	X_03_1 float64
	X_04   uint64
	X_05_0 float64
	X_05_1 float64
	X_05_2 float64
	X_06   uint64
	X_07   uint64
	X_08   uint64
	X_09   uint64
	X_0A_0 float64
	X_0A_1 float64
	X_0B   uint64
	X_0C   float64
	X_0D_0 uint64
	X_0D_1 uint64
	X_0D_2 uint64
	X_0D_3 uint64
	X_0E_0 float64
	X_0E_1 float64
	X_10   uint64
	X_11   float64
	X_12   uint64
	X_13   uint64
}

type modelDecoder func(IMT, string, string, time.Time) []record.SensorDataRecord

var modelDecoders = map[string]modelDecoder{
	"LNV3_SM3DL": decodeLNV3SM3DL,
	"LNV3_SVC":   decodeLNV3SVC,
}

// parseIMT decodes a raw IMT uplink payload into its tagged fields.
func parseIMT(payload []byte) IMT {
	var v IMT
	n := len(payload)
	_01, _03, _0d, _0e := 0, 0, 0, 0

PL:
	for i := 0; i < n; i++ {
		switch payload[i] {
		case 0x01:
			if i+2 >= n {
				break PL
			}
			f := be16(payload[i+1], payload[i+2]) / 10
			switch _01 {
			case 0:
				v.X_01_0 = f
			case 1:
				v.X_01_1 = f
			case 2:
				v.X_01_2 = f
			case 3:
				v.X_01_3 = f
			case 4:
				v.X_01_4 = f
			case 5:
				v.X_01_5 = f
			case 6:
				v.X_01_6 = f
			case 7:
				v.X_01_7 = f
			}
			_01++
			i += 2

		case 0x02:
			if i+2 >= n {
				break PL
			}
			v.X_02 = be16(payload[i+1], payload[i+2]) / 10
			i += 2

		case 0x03:
			if i+4 >= n {
				break PL
			}
			f := be16(payload[i+3], payload[i+4])
			switch _03 {
			case 0:
				v.X_03_0 = f
			case 1:
				v.X_03_1 = f
			}
			_03++
			i += 2

		case 0x05:
			if i+6 >= n {
				break PL
			}
			v.X_05_0 = be16(payload[i+1], payload[i+2]) / 10
			v.X_05_1 = be16(payload[i+3], payload[i+4]) / 10
			v.X_05_2 = be16(payload[i+5], payload[i+6]) / 10
			i += 6

		case 0x0A:
			if i+8 >= n {
				break PL
			}
			v.X_0A_0 = signedFraction(payload[i+1], payload[i+2], payload[i+3], payload[i+4])
			v.X_0A_1 = signedFraction(payload[i+5], payload[i+6], payload[i+7], payload[i+8])
			i += 8

		case 0x0B:
			if i+3 >= n {
				break PL
			}
			v.X_0B = uint64(payload[i+1])<<16 | uint64(payload[i+2])<<8 | uint64(payload[i+3])
			i += 3

		case 0x0C:
			if i+2 >= n {
				break PL
			}
			v.X_0C = be16(payload[i+1], payload[i+2]) / 1000
			i += 2

		case 0x0D:
			if i+2 >= n {
				break PL
			}
			raw := uint64(payload[i+1])<<8 | uint64(payload[i+2])
			switch _0d {
			case 0:
				v.X_0D_0 = raw
			case 1:
				v.X_0D_1 = raw
			case 2:
				v.X_0D_2 = raw
			case 3:
				v.X_0D_3 = raw
			}
			_0d++
			i += 2

		case 0x0E:
			if i+4 >= n {
				break PL
			}
			raw := uint64(payload[i+1])<<24 | uint64(payload[i+2])<<16 | uint64(payload[i+3])<<8 | uint64(payload[i+4])
			f := float64(raw) * (150.0 / 5.0) / 2000.0
			switch _0e {
			case 0:
				v.X_0E_0 = f
			case 1:
				v.X_0E_1 = f
			}
			_0e++
			i += 4

		case 0x10:
			if i+2 >= n {
				break PL
			}
			v.X_10 = uint64(payload[i+1])<<8 | uint64(payload[i+2])
			i += 2

		case 0x11:
			if i+2 >= n {
				break PL
			}
			v.X_11 = be16(payload[i+1], payload[i+2]) / 100
			i += 2

		case 0x13:
			if i+2 >= n {
				break PL
			}
			v.X_13 = uint64(payload[i+1])<<8 | uint64(payload[i+2])
			i += 2

		default:
			break PL
		}
	}
	return v
}

// be16 combines two big-endian bytes into a float64.
func be16(hi, lo byte) float64 {
	return float64(uint64(hi)<<8 | uint64(lo))
}

// signedFraction decodes IMT's sign-byte + 3-byte micro-fraction encoding
// used by tag 0x0A: sign is a two's-complement int8 whole part, frac is a
// 24-bit unsigned fractional part scaled by 1e6.
func signedFraction(sign, f2, f1, f0 byte) float64 {
	whole := uint64(sign)
	frac := uint64(f2)<<16 | uint64(f1)<<8 | uint64(f0)
	b := float64(frac) / 1000000
	if whole > 127 {
		return -((255 - float64(whole)) + 1) - b
	}
	return float64(whole) + b
}

// Decode routes an IMT payload to the registered model+submodel decoder.
func Decode(deviceModel, deviceSubmodel string, payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	data := parseIMT(payload)
	key := strings.ToUpper(strings.TrimSpace(deviceModel))
	if sub := strings.ToUpper(strings.TrimSpace(deviceSubmodel)); sub != "" {
		key += "_" + sub
	}
	if decoder, ok := modelDecoders[key]; ok {
		return decoder(data, deviceID, provider, ts)
	}
	log.Printf("[imt] no decoder registered for model %s submodel %s", deviceModel, deviceSubmodel)
	return nil
}
