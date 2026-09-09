package registry

import (
	"strings"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/devices/imt"
	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/devices/khomp"
	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/devices/kron"
	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/devices/milesight"
	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

type customParser func(model, submodel, message, deviceID string) []record.SensorDataRecord
type lnsParser func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord

var customParsers = map[string]customParser{
	"KS3000_WIFI": func(model, submodel, message, deviceID string) []record.SensorDataRecord {
		return kron.Parse(model, submodel, message, deviceID)
	},
}

var lnsParsers = map[string]lnsParser{
	"DTL200": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return khomp.Decode(model, submodel, payload, deviceID, provider, port, ts)
	},
	"NIT21LI_EMW104": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return khomp.Decode(model, submodel, payload, deviceID, provider, port, ts)
	},
	"KS3000_LORA": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return kron.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	"EM300_DI": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return milesight.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	"EM500_SWL": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return milesight.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	"WS101": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return milesight.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	// WS101_SOS/WS101_SCENE: same wire protocol and decoder as WS101 (the
	// button's color/purpose isn't reported in the payload) — split by
	// device_model anyway, since it's a real hardware/deployment difference
	// worth having as its own queryable InfluxDB tag (e.g. "alert on any SOS
	// press across every SOS unit" without a per-device lookup). See
	// devices.json's comment on this device for the rationale.
	"WS101_SOS": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return milesight.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	"WS101_SCENE": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return milesight.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	"EM500_SMTC": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return milesight.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	"VS373": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return milesight.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	"AT101": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return milesight.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	"LNV3_SM3DL": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return imt.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	"LNV3_SVC": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return imt.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	"UC100": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return milesight.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	"UC300": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return milesight.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	"UC501": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return milesight.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	"UC511": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return milesight.Decode(model, submodel, payload, deviceID, provider, ts)
	},
	"VS370": func(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) []record.SensorDataRecord {
		return milesight.Decode(model, submodel, payload, deviceID, provider, ts)
	},
}

func modelKey(model, submodel string) string {
	if submodel == "" {
		return strings.ToUpper(model)
	}
	return strings.ToUpper(model) + "_" + strings.ToUpper(submodel)
}

func lookupKeys(model, submodel string) []string {
	if strings.TrimSpace(submodel) == "" {
		return []string{strings.ToUpper(model)}
	}
	return []string{modelKey(model, submodel), strings.ToUpper(model)}
}

// ParseCustom routes custom JSON payloads by model/submodel.
func ParseCustom(model, submodel, message, deviceID string) ([]record.SensorDataRecord, bool) {
	for _, k := range lookupKeys(model, submodel) {
		if parser, ok := customParsers[k]; ok {
			return parser(model, submodel, message, deviceID), true
		}
	}
	return nil, false
}

// DecodeLNS routes LNS binary payloads by model/submodel.
func DecodeLNS(model, submodel string, payload []byte, deviceID, provider string, port uint64, ts time.Time) ([]record.SensorDataRecord, bool) {
	for _, k := range lookupKeys(model, submodel) {
		if parser, ok := lnsParsers[k]; ok {
			return parser(model, submodel, payload, deviceID, provider, port, ts), true
		}
	}
	return nil, false
}
