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
     → buildSensorPoints → batchWriter.Enqueue → InfluxDB3
```

## Write batching

Decode workers run fully in parallel (`WRITE_WORKERS`, default 2×GOMAXPROCS)
but never call InfluxDB3 directly — each hands its points to a shared
`batchWriter` over a channel. One goroutine per database (`sensor_data`,
`raw`) is the only thing that ever calls `WritePoints`, flushing on a timer
(`WRITE_FLUSH_INTERVAL_SEC`, default 5s) or once buffered points reach
`WRITE_BATCH_SIZE` (default 5000), whichever comes first. This exists because
one `WritePoints` call per MQTT message from every worker independently
fragmented InfluxDB3's Parquet files within a single time window, inflating
the file count time-range queries must scan past InfluxDB3's file-scan cap.
Batch failures retry, then fall back to per-point writes; shutdown (signal or
MQTT connection loss) drains and flushes whatever's buffered before exit.

Device registry is fetched from the SmartCampusMaua `api` repo's registry
endpoints (`GET {REGISTRY_API_URL}/api/registry/v0.1/devices`, plain
unauthenticated GET) at startup and hot-reloaded every
`DEVICE_REGISTRY_REFRESH_SEC` seconds (default 30) without restarting. Assets/
sensors/Modbus-channels come from that same API's `/assets`, `/sensors`, and
`/modbus-channels` endpoints.

## Connection config

InfluxDB3 host/token, MQTT broker/credentials, and the registry API's base
URL (`registry.api_url`) load from `infra.json`
(`cmd/parse-mqtt-to-influxdb3/infra_config.go`), mirroring the SmartCampusMaua
`api` repo's `influxdb.json`/`config.ts` pattern exactly, `influxdb.orgs`
array included — this service only ever has one InfluxDB host+token pair,
so it just picks the first `orgs` entry not disabled via `"allow": false`
and ignores the rest. Fails fast on a missing/invalid file at startup.
Gitignored, holds a live token — see `infra.example.json` for the shape. Polled every
`INFRA_CONFIG_REFRESH_SEC` (default 30s); a change triggers a full graceful
restart (drain, flush, exit) rather than hot-swapping the live InfluxDB3/MQTT
clients — requires the volume mounted as a whole directory, never `subPath`.
`INFRA_CONFIG_FILE`'s path is baked into `docker/Dockerfile`
(`/etc/parse-mqtt-to-influxdb3/infra.json`), mirroring `api`'s own `ENV
INFLUXDB_CONFIG_FILE=/etc/api/influxdb.json` — a Kubernetes manifest should
never set this env var itself, only mount the volume at that path.

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

| Map key            | device_model (registry API)                  |
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
4. Register the device via `POST /api/registry/v0.1/devices` on the registry API with `"device_model": "model_submodel"` (already merged, lowercase)

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
go test ./...
```

No external test infrastructure required — the unit tests use in-process
fakes (e.g. `batch_writer_test.go`'s fake `influxWriter`) rather than a live
MQTT broker or InfluxDB3 instance.
