// Package kron parses and decodes Kron payloads.
//
// Naming pattern:
//   - Parse*  = JSON payload parsing
//   - Decode* = binary payload decoding
package kron

import (
	"log"
	"strings"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/internal/record"
)

type Metadata struct {
	U0  float64 `json:"U0"`
	I0  float64 `json:"I0"`
	F1  float64 `json:"F1"`
	P0  float64 `json:"P0"`
	Q0  float64 `json:"Q0"`
	FP0 float64 `json:"FP0"`
	EA  float64 `json:"EA"`
	ER  float64 `json:"ER"`
	EAN float64 `json:"EAN"`
	ERN float64 `json:"ERN"`
	CE  float64 `json:"CE"`
}

type Message struct {
	Variable string   `json:"variable"`
	Time     string   `json:"time"`
	Metadata Metadata `json:"metadata"`
}

type jsonParser func(string, string, string) []record.SensorDataRecord
type binaryDecoder func([]byte, string, string, time.Time) []record.SensorDataRecord

var jsonParsers = map[string]jsonParser{
	"KS3000_WIFI": parseKS3000WiFi,
}

var binaryDecoders = map[string]binaryDecoder{
	"KS3000_LORA": decodeKS3000LORA,
}

func parserKey(model, submodel string) string {
	if submodel == "" {
		return strings.ToUpper(model)
	}
	return strings.ToUpper(model) + "_" + strings.ToUpper(submodel)
}

func buildRecords(md Metadata, dm, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	st := record.ST
	var out []record.SensorDataRecord
	if md.U0 != 0 {
		out = append(out, record.NewFloat(st.VoltageULLAvg, dm, deviceID, provider, md.U0, ts))
	}
	if md.I0 != 0 {
		out = append(out, record.NewFloat(st.CurrentIAvg, dm, deviceID, provider, md.I0, ts))
	}
	if md.F1 != 0 {
		out = append(out, record.NewFloat(st.Frequency, dm, deviceID, provider, md.F1, ts))
	}
	if md.P0 != 0 {
		out = append(out, record.NewFloat(st.PowerPTotal, dm, deviceID, provider, md.P0, ts))
	}
	if md.Q0 != 0 {
		out = append(out, record.NewFloat(st.PowerQTotal, dm, deviceID, provider, md.Q0, ts))
	}
	if md.FP0 != 0 {
		out = append(out, record.NewFloat(st.PowerFactor, dm, deviceID, provider, md.FP0, ts))
	}
	if md.EA != 0 {
		out = append(out, record.NewFloat(st.EnergyAPlus, dm, deviceID, provider, md.EA, ts))
	}
	if md.ER != 0 {
		out = append(out, record.NewFloat(st.EnergyQPlus, dm, deviceID, provider, md.ER, ts))
	}
	if md.EAN != 0 {
		out = append(out, record.NewFloat(st.EnergyAMinus, dm, deviceID, provider, md.EAN, ts))
	}
	if md.ERN != 0 {
		out = append(out, record.NewFloat(st.EnergyQMinus, dm, deviceID, provider, md.ERN, ts))
	}
	out = append(out, record.NewFloat(st.ErrorCode, dm, deviceID, provider, md.CE, ts))
	return out
}

// Parse routes a JSON payload to the registered Kron parser.
func Parse(deviceModel, deviceSubmodel, message, deviceID string) []record.SensorDataRecord {
	parser, ok := jsonParsers[parserKey(deviceModel, deviceSubmodel)]
	if !ok {
		log.Printf("[kron] no JSON parser registered for model %s submodel %s", deviceModel, deviceSubmodel)
		return nil
	}
	return parser(message, deviceID, deviceModel)
}

// Decode routes a binary payload to the registered Kron decoder.
func Decode(deviceModel, deviceSubmodel string, payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord {
	decoder, ok := binaryDecoders[parserKey(deviceModel, deviceSubmodel)]
	if !ok {
		log.Printf("[kron] no binary decoder registered for model %s submodel %s", deviceModel, deviceSubmodel)
		return nil
	}
	return decoder(payload, deviceID, provider, ts)
}
