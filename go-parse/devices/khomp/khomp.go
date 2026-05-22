// Package khomp parses and decodes Khomp-related payloads.
//
// Naming pattern:
//   - Parse*  = JSON payload parsing
//   - Decode* = binary payload decoding
package khomp

import (
	"log"
	"strings"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/internal/record"
)

type binaryDecoder func([]byte, string, string, uint64, time.Time) []record.SensorDataRecord

var binaryDecoders = map[string]binaryDecoder{
	"NIT21LI_EMW104": decodeNIT21LIEMW104,
	"DTL200":         decodeDTL200,
}

func decoderKey(model, submodel string) string {
	if submodel == "" {
		return strings.ToUpper(model)
	}
	return strings.ToUpper(model) + "_" + strings.ToUpper(submodel)
}

// Decode routes a binary payload to the registered Khomp decoder.
func Decode(deviceModel, deviceSubmodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
	decoder, ok := binaryDecoders[decoderKey(deviceModel, deviceSubmodel)]
	if !ok {
		log.Printf("[khomp] no binary decoder registered for model %s submodel %s", deviceModel, deviceSubmodel)
		return nil
	}
	return decoder(payload, deviceID, provider, port, ts)
}
