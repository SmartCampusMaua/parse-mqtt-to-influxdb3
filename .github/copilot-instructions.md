# GitHub Copilot instructions — parse-mqtt-to-influxdb3

## Project summary

Go service that subscribes to MQTT, auto-detects LNS provider (ChirpStack v4,
Everynet, or custom direct-MQTT), decodes binary/JSON payloads, and writes
`sensor_data` points to InfluxDB3 Core. No ORM, no frameworks — standard library
plus a handful of well-known Go packages.

## Architecture

```
MQTT → detectProvider → resolveIncomingDevice → parseMsg
     → (chirpstack|everynet).Parse → LNSFrame
     → registry.DecodeLNS / registry.ParseCustom
     → vendor.Decode / vendor.Parse
     → []SensorDataRecord
     → writeSensorRecords → InfluxDB3
```

Device registry is loaded from `devices.json` at startup and hot-reloaded every
`DEVICE_REGISTRY_REFRESH_SEC` seconds (default 30) without restarting.

## Decoder file layout

One file per device codec. Naming: `model_submodel.go` (lowercase).

```
go-parse/devices/
  khomp/
    khomp.go              vendor router: Decode(model, submodel, ...)
    dtl200.go             DTL200 — key "DTL"
    nit21li_emw104.go     NIT21LI+EMW104 — key "NIT21LI_EMW104"
  kron/
    kron.go               vendor router: Parse + Decode
    ks3000_lora.go        KS3000 LoRa — key "KS3000_LORA"
    ks3000_wifi.go        KS3000 WiFi — key "KS3000_WIFI"
  milesight/
    milesight.go          ParseMilesightTLV + Decode(model, submodel, ...)
    em300_di.go           EM300-DI — key "EM300_DI"
    em500_swl.go          EM500-SWL — key "EM500_SWL"
    ws101.go              WS101 — key "WS101"
  registry/
    registry.go           central routing: ParseCustom + DecodeLNS
```

## Routing keys

Registry and vendor maps use `MODEL_SUBMODEL` (uppercase). Fallback to `MODEL`
is handled automatically by `lookupKeys()` in `registry.go`.

| Map key            | devices.json                                 |
| ------------------ | -------------------------------------------- |
| `"DTL"`            | `deviceModel=DTL` (no submodel)              |
| `"NIT21LI_EMW104"` | `deviceModel=NIT21LI, deviceSubmodel=EMW104` |
| `"KS3000_LORA"`    | `deviceModel=KS3000, deviceSubmodel=LORA`    |
| `"KS3000_WIFI"`    | `deviceModel=KS3000, deviceSubmodel=WIFI`    |
| `"EM300_DI"`       | `deviceModel=EM300, deviceSubmodel=DI`       |
| `"EM500_SWL"`      | `deviceModel=EM500, deviceSubmodel=SWL`      |
| `"WS101"`          | `deviceModel=WS101` (no submodel)            |

## InfluxDB3 schema

Measurement `sensor_data`:

- Tags: `sensor_type`, `device_model`, `device_id`, `provider`, `dev_eui`\*
- Fields: `value_float` OR `value_int` OR `value_bool` (exactly one per row)
- `device_model` = lowercase merged `model_submodel` (e.g. `em300_di`, `ks3000_lora`)
- `dev_eui` written only for LoRaWAN providers

Measurement `raw` in `audit_iot`:

- Tags: `device_id`, `event_type`
- Field: `raw_data` (string ≤512 chars)

`sensor_data` and `raw` always hold exactly the value transmitted by the sensor,
untouched — no calibration/unit-conversion happens in this pipeline. That's a
client-side/user-space concern for whatever consumes the SmartCampusMaua API.

Four databases on same host/token: `iot_sensors`, `audit_iot`, `vehicle_telemetry`, `audit_vehicle`.

## Adding a new device

1. Create `go-parse/devices/<vendor>/<model>_<submodel>.go`
   - implement `decode<ModelSubmodel>(entries []TLV, deviceID, provider string, ts time.Time) []record.SensorDataRecord`
   - export `Decode<ModelSubmodel>(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord`
2. Add `"MODEL_SUBMODEL": decode<ModelSubmodel>` to the vendor's decoder map
3. Add `"MODEL_SUBMODEL": ...vendor.Decode...` to `lnsParsers` in `registry/registry.go`
4. Add entries to `devices.json` with `"deviceModel": "MODEL"` and `"deviceSubmodel": "SUBMODEL"`

## Conventions

- `sensor_type` values: defined in `record.SensorTypes` (field on the `ST` singleton) — never bare strings
- Pattern: `<domain>_<abbreviation>` — e.g. `air_temp`, `air_rh`, `solar_rad`, `air_press`
- `const dm = "model"` in decoders sets the `DeviceModel` field; submodel is appended at write time
- Do not add submodel dispatch maps inside decoder files; one file = one decoder function
- Do not create placeholder files for unimplemented devices
- Use `strings.ToUpper` for map key comparisons, never case-fold the stored key itself

## Build and test

```sh
go build ./...
go vet ./...
```

No external test infrastructure required. The build failing is the signal.
