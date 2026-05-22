package khomp

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/internal/record"
)

// NIT21LISensors holds all decoded sensor fields from a Khomp NIT 21LI payload.
type NIT21LISensors struct {
	InternalBatteryVoltage float64
	PowerSource            bool
	FirmwareVersion        uint64
	EnvSensorFailStatus    bool
	C1State                bool
	C1Count                uint64
	C2State                bool
	C2Count                uint64
	InternalTemperature    float64
	InternalHumidity       float64
	EmwRainLevel           float64
	EmwAvgWindSpeed        float64
	EmwGustWindSpeed       float64
	EmwWindDirection       float64
	EmwTemperature         float64
	EmwHumidity            float64
	EmwLuminosity          float64
	EmwUv                  float64
	EmwSolarRadiation      float64
	EmwAtmPres             float64

	IsEnvSensorFailStatus    bool
	IsInternalBatteryVoltage bool
	IsFirmwareVersion        bool
	IsInternalTemperature    bool
	IsInternalHumidity       bool
	IsC1State                bool
	IsC1Count                bool
	IsC2State                bool
	IsC2Count                bool
	IsEmwRainLevel           bool
	IsEmwAvgWindSpeed        bool
	IsEmwGustWindSpeed       bool
	IsEmwWindDirection       bool
	IsEmwTemperature         bool
	IsEmwHumidity            bool
	IsEmwLuminosity          bool
	IsEmwUv                  bool
	IsEmwSolarRadiation      bool
	IsEmwAtmPres             bool
}

func roundFloat(val float64, precision uint) float64 {
	ratio := math.Pow(10, float64(precision))
	return math.Round(val*ratio) / ratio
}

// parseNIT21LISensors decodes Khomp fPort-4 binary payload into sensor fields.
func parseNIT21LISensors(bytes []byte) (*NIT21LISensors, error) {
	p := &NIT21LISensors{}
	index := 0

	maskSensorInt := bytes[index]
	index++
	var maskSensorIntE byte
	if maskSensorInt>>7&0x01 == 0x01 {
		maskSensorIntE = bytes[index]
		p.IsEnvSensorFailStatus = true
		index++
	}
	maskSensorExt := bytes[index]
	index++

	if maskSensorIntE>>0&0x01 == 0x01 {
		p.EnvSensorFailStatus = true
	}

	if maskSensorInt>>0&0x01 == 0x01 {
		p.IsInternalBatteryVoltage = true
		if maskSensorInt>>6&0x01 == 0x01 {
			p.InternalBatteryVoltage = roundFloat(float64((bytes[index]/120.0)+1), 2)
		} else {
			p.InternalBatteryVoltage = roundFloat(float64(bytes[index]/10.0), 1)
		}
		index++
	}

	if maskSensorInt>>2&0x01 == 0x01 {
		p.IsFirmwareVersion = true
		v := uint64(bytes[index])
		v |= uint64(bytes[index+2]) << 8
		v |= uint64(bytes[index+3]) << 16
		p.FirmwareVersion = v / 1000000
		index += 3
	}

	p.PowerSource = maskSensorInt>>5&0x01 == 0x01

	if maskSensorInt>>3&0x01 == 0x01 {
		p.IsInternalTemperature = true
		v := uint64(bytes[index]) | uint64(bytes[index+1])<<8
		p.InternalTemperature = roundFloat((float64(v)/100)-273.15, 2)
		index += 2
	}

	if maskSensorInt>>4&0x01 == 0x01 {
		p.IsInternalHumidity = true
		v := uint64(bytes[index]) | uint64(bytes[index+1])<<8
		p.InternalHumidity = roundFloat(float64(v)/10, 2)
		index += 2
	}

	if maskSensorExt>>0&0x01 == 0x01 {
		p.IsC1State = true
		p.C1State = bytes[index] == 0x01
		index++
	}
	if maskSensorExt>>1&0x01 == 0x01 {
		p.IsC1Count = true
		p.C1Count = uint64(bytes[index]) | uint64(bytes[index+1])<<8
		index += 2
	}
	if maskSensorExt>>2&0x01 == 0x01 {
		p.IsC2State = true
		p.C2State = bytes[index] == 0x01
		index++
	}
	if maskSensorExt>>3&0x01 == 0x01 {
		p.IsC2Count = true
		p.C2Count = uint64(bytes[index]) | uint64(bytes[index+1])<<8
		index += 2
	}

	if index < len(bytes) && bytes[index] == 0x04 {
		index++
		maskEmw104 := bytes[index]
		index++

		if maskEmw104>>0&0x01 == 0x01 {
			p.IsEmwRainLevel = true
			v := uint64(bytes[index])<<8 | uint64(bytes[index+1])
			p.EmwRainLevel = roundFloat(float64(v)/10, 1)
			index += 2
			p.IsEmwAvgWindSpeed = true
			p.EmwAvgWindSpeed = roundFloat(float64(bytes[index]), 1)
			index++
			p.IsEmwGustWindSpeed = true
			p.EmwGustWindSpeed = roundFloat(float64(bytes[index]), 1)
			index++
			p.IsEmwWindDirection = true
			v = uint64(bytes[index])<<8 | uint64(bytes[index+1])
			p.EmwWindDirection = roundFloat(float64(v), 1)
			index += 2
			p.IsEmwTemperature = true
			v = uint64(bytes[index])<<8 | uint64(bytes[index+1])
			p.EmwTemperature = roundFloat(float64(v)/10-273.15, 2)
			index += 2
			p.IsEmwHumidity = true
			p.EmwHumidity = roundFloat(float64(bytes[index]), 1)
			index++
		}
		if maskEmw104>>1&0x01 == 0x01 {
			p.IsEmwLuminosity = true
			v := uint64(bytes[index])<<16 | uint64(bytes[index+1])<<8 | uint64(bytes[index+2])
			p.EmwLuminosity = roundFloat(float64(v), 1)
			p.IsEmwUv = true
			p.EmwUv = roundFloat(float64(bytes[index+3])/10, 1)
			index += 4
		}
		if maskEmw104>>2&0x01 == 0x01 {
			p.IsEmwSolarRadiation = true
			v := uint64(bytes[index])<<8 | uint64(bytes[index+1])
			p.EmwSolarRadiation = roundFloat(float64(v)/10, 1)
			index += 2
		}
		if maskEmw104>>3&0x01 == 0x01 {
			p.IsEmwAtmPres = true
			v := uint64(bytes[index])<<16 | uint64(bytes[index+1])<<8 | uint64(bytes[index+2])
			p.EmwAtmPres = roundFloat(float64(v)/100, 2)
		}
	} else if index < len(bytes) {
		fmt.Printf("[khomp] unrecognised extension module 0x%02X\n", bytes[index])
	}

	return p, nil
}

func decodeNIT21LIEMW104(payload []byte, deviceID, provider string, _ uint64, ts time.Time) []record.SensorDataRecord {
	const dm = "nit21li_emw104"
	s, err := parseNIT21LISensors(payload)
	if err != nil {
		log.Printf("[khomp] decodeNIT21LIEMW104[%s]: %v", deviceID, err)
		return nil
	}
	st := record.ST
	var out []record.SensorDataRecord

	out = append(out, record.NewBool(st.ExternalPower, dm, deviceID, provider, s.PowerSource, ts))
	if s.IsEnvSensorFailStatus {
		out = append(out, record.NewBool(st.EnvSensorFailStatus, dm, deviceID, provider, s.EnvSensorFailStatus, ts))
	}
	if s.IsInternalBatteryVoltage {
		out = append(out, record.NewFloat(st.InternalBatteryVoltage, dm, deviceID, provider, s.InternalBatteryVoltage, ts))
	}
	if s.IsC1State {
		out = append(out, record.NewBool(st.C1State, dm, deviceID, provider, s.C1State, ts))
	}
	if s.IsC1Count {
		out = append(out, record.NewInt(st.C1Count, dm, deviceID, provider, int64(s.C1Count), ts))
	}
	if s.IsC2State {
		out = append(out, record.NewBool(st.C2State, dm, deviceID, provider, s.C2State, ts))
	}
	if s.IsC2Count {
		out = append(out, record.NewInt(st.C2Count, dm, deviceID, provider, int64(s.C2Count), ts))
	}
	if s.IsInternalTemperature {
		out = append(out, record.NewFloat(st.InternalTemp, dm, deviceID, provider, s.InternalTemperature, ts))
	}
	if s.IsInternalHumidity {
		out = append(out, record.NewFloat(st.InternalRH, dm, deviceID, provider, s.InternalHumidity, ts))
	}
	if s.IsEmwRainLevel {
		out = append(out, record.NewFloat(st.RainDepth, dm, deviceID, provider, s.EmwRainLevel, ts))
	}
	if s.IsEmwAvgWindSpeed {
		out = append(out, record.NewFloat(st.WindSpeed, dm, deviceID, provider, s.EmwAvgWindSpeed, ts))
	}
	if s.IsEmwGustWindSpeed {
		out = append(out, record.NewFloat(st.WindGust, dm, deviceID, provider, s.EmwGustWindSpeed, ts))
	}
	if s.IsEmwWindDirection {
		out = append(out, record.NewFloat(st.WindDir, dm, deviceID, provider, s.EmwWindDirection, ts))
	}
	if s.IsEmwTemperature {
		out = append(out, record.NewFloat(st.AirTemp, dm, deviceID, provider, s.EmwTemperature, ts))
	}
	if s.IsEmwHumidity {
		out = append(out, record.NewFloat(st.AirRH, dm, deviceID, provider, s.EmwHumidity, ts))
	}
	if s.IsEmwLuminosity {
		out = append(out, record.NewFloat(st.Illuminance, dm, deviceID, provider, s.EmwLuminosity, ts))
	}
	if s.IsEmwUv {
		out = append(out, record.NewFloat(st.UVIndex, dm, deviceID, provider, s.EmwUv, ts))
	}
	if s.IsEmwSolarRadiation {
		out = append(out, record.NewFloat(st.SolarRad, dm, deviceID, provider, s.EmwSolarRadiation, ts))
	}
	if s.IsEmwAtmPres {
		out = append(out, record.NewFloat(st.AirPress, dm, deviceID, provider, s.EmwAtmPres, ts))
	}
	return out
}

// DecodeNIT21LIEMW104 decodes a Khomp NIT21LI + EMW104 binary payload.
func DecodeNIT21LIEMW104(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	return decodeNIT21LIEMW104(payload, deviceID, provider, 0, ts)
}

// jsonNIT21LISensors wraps NIT21LISensors for JSON serialisation (legacy compat).
func jsonNIT21LISensors(ks *NIT21LISensors) string {
	b, err := json.Marshal(ks)
	if err != nil {
		return ""
	}
	return string(b)
}
