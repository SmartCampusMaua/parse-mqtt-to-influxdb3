# CLAUDE.md — parse-mqtt-to-influxdb3

Instructions for AI coding assistants working in this repository.

## Project

Go service. Subscribes to MQTT → decodes binary/JSON payloads → writes
`sensor_data` points to InfluxDB3 Core. No ORM, no frameworks.

## Decode chain

```
Registry API (GET {REGISTRY_API_URL}/api/registry/v0.1/devices) → GetDevicesMap() → deviceMap / devEUIToDeviceID
MQTT message → detectProvider → resolveIncomingDevice → parseMsg
           → chirpstack/everynet.Parse → LNSFrame
           → registry.DecodeLNS(model, submodel, payload, ...)
               → lookupKeys: ["MODEL_SUBMODEL", "MODEL"]
               → lnsParsers["MODEL_SUBMODEL"] → vendor.Decode()
                   → binaryDecoders["MODEL_SUBMODEL"] → decode<X>()
                       → []SensorDataRecord
           → buildSensorPoints → sensorWriter.Enqueue (batchWriter) → InfluxDB3 iot_sensors
```

## Write batching

Decode workers never call InfluxDB3 directly. Each hands its points to a
shared `batchWriter` (`cmd/parse-mqtt-to-influxdb3/main.go`) over a channel;
one goroutine per database (`sensor_data`, `raw`) is the only thing that ever
calls `WritePoints`, flushing on a timer (`WRITE_FLUSH_INTERVAL_SEC`, default
5s) or a size threshold (`WRITE_BATCH_SIZE`, default 5000 points), whichever
comes first. This exists because one `WritePoints` call per MQTT message from
every worker independently fragments InfluxDB3's Parquet files across a time
window, inflating the file count time-range queries must scan. Batch write
failures retry, then fall back to per-point writes; shutdown (signal or MQTT
connection loss) drains and flushes buffered points before exit.

## Connection config

InfluxDB3 host/token, MQTT broker/credentials, and the registry API's base
URL (`registry.api_url`) load from `infra.json`
(`cmd/parse-mqtt-to-influxdb3/infra_config.go`), not env vars — mirrors the
`api` repo's `influxdb.json`/`config.ts` pattern exactly, `influxdb.orgs`
array included. This service only ever has one InfluxDB host+token pair, so
it just picks the first `orgs` entry not disabled via `"allow": false` and
ignores the rest. Gitignored, holds a live token; see `infra.example.json`
for the shape. Fails fast on a
bad/missing file at startup. Polled every `INFRA_CONFIG_REFRESH_SEC`
(default 30s); a change triggers a full graceful process restart (drain,
flush, exit) rather than hot-swapping the live InfluxDB3/MQTT clients —
safe in Kubernetes since the pod's restart policy brings it back up with
the new config, but requires the volume mounted as a whole directory, never
`subPath` (which never updates in place).

`INFRA_CONFIG_FILE`'s path is baked into `docker/Dockerfile`
(`/etc/parse-mqtt-to-influxdb3/infra.json`), same pattern as `api`'s own
`ENV INFLUXDB_CONFIG_FILE=/etc/api/influxdb.json` — a Kubernetes manifest
should never set this env var itself, only mount the volume at that path.

## File layout — one file per device codec

```
go-parse/devices/
  khomp/   khomp.go  dtl200.go  nit21li_emw104.go
  kron/    kron.go   ks3000_lora.go  ks3000_wifi.go
  milesight/  milesight.go  em300_di.go  em500_swl.go  ws101.go
  registry/   registry.go
```

## Routing key table (always uppercase)

| File                | Vendor map key     | Registry key       | device_model (registry API)                  |
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
Merge happens in `buildSensorPoints` — decoders use `const dm = "em300"` (base model only).
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
4. Register the device via `POST /api/registry/v0.1/devices` on the registry API with `"device_model": "model_submodel"` (already merged, lowercase)

## Build

```sh
go build ./...
go vet ./...
go test ./...
```
