# CLAUDE.md — parse-mqtt-to-influxdb3

Instructions for AI coding assistants working in this repository.

## Project

Go service. Subscribes to MQTT → decodes binary/JSON payloads → writes
`sensor_data` points to InfluxDB3 Core. No ORM, no frameworks.

## Decode chain

```
devices.json → GetDevicesMap() → deviceMap / devEUIToDeviceID
MQTT message → detectProvider → resolveIncomingDevice → parseMsg
           → chirpstack/everynet.Parse → LNSFrame
           → registry.DecodeLNS(model, submodel, payload, ...)
               → lookupKeys: ["MODEL_SUBMODEL", "MODEL"]
               → lnsParsers["MODEL_SUBMODEL"] → vendor.Decode()
                   → binaryDecoders["MODEL_SUBMODEL"] → decode<X>()
                       → []SensorDataRecord
           → writeSensorRecords → InfluxDB3 iot_sensors
```

## File layout — one file per device codec

```
go-parse/devices/
  khomp/   khomp.go  dtl200.go  nit21li_emw104.go
  kron/    kron.go   ks3000_lora.go  ks3000_wifi.go
  milesight/  milesight.go  em300_di.go  em500_swl.go  ws101.go
  registry/   registry.go
```

## Routing key table (always uppercase)

| File                | Vendor map key     | Registry key       | devices.json                                 |
| ------------------- | ------------------ | ------------------ | -------------------------------------------- |
| `dtl200.go`         | `"DTL"`            | `"DTL"`            | `deviceModel=DTL`                            |
| `nit21li_emw104.go` | `"NIT21LI_EMW104"` | `"NIT21LI_EMW104"` | `deviceModel=NIT21LI, deviceSubmodel=EMW104` |
| `ks3000_lora.go`    | `"KS3000_LORA"`    | `"KS3000_LORA"`    | `deviceModel=KS3000, deviceSubmodel=LORA`    |
| `ks3000_wifi.go`    | `"KS3000_WIFI"`    | `"KS3000_WIFI"`    | `deviceModel=KS3000, deviceSubmodel=WIFI`    |
| `em300_di.go`       | `"EM300_DI"`       | `"EM300_DI"`       | `deviceModel=EM300, deviceSubmodel=DI`       |
| `em500_swl.go`      | `"EM500_SWL"`      | `"EM500_SWL"`      | `deviceModel=EM500, deviceSubmodel=SWL`      |
| `ws101.go`          | `"WS101"`          | `"WS101"`          | `deviceModel=WS101`                          |

## InfluxDB3 tag schema

`sensor_data` tags: `sensor_type`, `device_model`, `device_id`, `provider`, `dev_eui`\*

`device_model` = lowercase merged model+submodel (e.g. `em300_di`, `nit21li_emw104`).
Merge happens in `writeSensorRecords` — decoders use `const dm = "em300"` (base model only).
`dev_eui` written only for LoRaWAN providers.

## Conventions

- `sensor_type` values: always from `record.ST` (the `SensorTypes` singleton) — never bare strings
- `const dm` in decoder files = lowercase base model name (e.g. `"em300"`, `"nit21li"`)
- One file = one decoder function. No internal submodel dispatch maps inside decoder files.
- Decoder signature (LNS binary): `decode<X>(entries []TLV, deviceID, provider string, ts time.Time) []record.SensorDataRecord`
- Public API: `Decode<X>(payload []byte, deviceID, provider string, ts time.Time) []record.SensorDataRecord`
- Do not create placeholder files for unimplemented devices
- Registry `lookupKeys()` tries `MODEL_SUBMODEL` first, then falls back to `MODEL` — no need to register both

## Adding a new device

1. `go-parse/devices/<vendor>/<model>_<submodel>.go` — implement `decode<X>` and `Decode<X>`
2. Add `"MODEL_SUBMODEL": decode<X>` to vendor map in `<vendor>.go`
3. Add `"MODEL_SUBMODEL": ...vendor.Decode...` to `lnsParsers` in `registry/registry.go`
4. Add entries with `"deviceModel": "MODEL", "deviceSubmodel": "SUBMODEL"` to `devices.json`

## Build

```sh
go build ./...
go vet ./...
```
