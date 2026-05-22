# parse-mqtt-to-influxdb3

Subscribes to MQTT, auto-detects the message provider, decodes the binary or
JSON payload, and writes one **`sensor_data`** point per decoded field directly
to an InfluxDB3 Core instance.

---

## InfluxDB3 measurement schema

### `sensor_data` — measurement written by all device decoders

| element     | value                                                                      |
| ----------- | -------------------------------------------------------------------------- |
| measurement | `sensor_data`                                                              |
| tags        | `sensor_type`, `device_model`, `device_id`, `provider`, `dev_eui`\*        |
| fields      | `value_float` **or** `value_int` **or** `value_bool` (exactly one per row) |
| timestamp   | nanosecond precision, sourced from the LNS frame or device message         |

`device_model` is the **merged** model+submodel key in lowercase, e.g.:

| `deviceModel` in devices.json | `device_model` tag in InfluxDB3 |
| ----------------------------- | ------------------------------- |
| `"EM300_DI"`                  | `em300_di`                      |
| `"EM500_SWL"`                 | `em500_swl`                     |
| `"NIT21LI_EMW104"`            | `nit21li_emw104`                |
| `"KS3000_LORA"`               | `ks3000_lora`                   |
| `"KS3000_WIFI"`               | `ks3000_wifi`                   |
| `"DTL"`                       | `dtl`                           |
| `"WS101"`                     | `ws101`                         |

This single merged tag eliminates a redundant index dimension. Queries are
`WHERE device_model = 'em300_di'`; to match all EM300 variants use
`WHERE device_model LIKE 'em300%'`.

`*` `dev_eui` is written only for LoRaWAN providers (`chirpstackv4`, `everynet`).

### `raw` — audit measurement in `audit_iot` database

| element     | value                                          |
| ----------- | ---------------------------------------------- |
| measurement | `raw`                                          |
| tags        | `device_id`, `event_type = "payload_ingest"`   |
| fields      | `raw_data` (string ≤ 512 chars)                |
| timestamp   | ingestion time (time.Now() at message receipt) |

### `sensor_calibration` — written once at startup

| element     | value                                              |
| ----------- | -------------------------------------------------- |
| measurement | `sensor_calibration`                               |
| tags        | `device_id`, `sensor_type`                         |
| fields      | `scale` (float), `offset` (float), `power` (float) |

Formula applied by users at query time: `calibrated = (raw ^ power) × scale + offset`

---

## Databases

Four InfluxDB3 databases are initialised simultaneously on the same host and token:

| database            | content                             |
| ------------------- | ----------------------------------- |
| `iot_sensors`       | `sensor_data`, `sensor_calibration` |
| `audit_iot`         | `raw` (LoRaWAN / MQTT audit log)    |
| `vehicle_telemetry` | vehicle GPS/CAN telemetry           |
| `audit_vehicle`     | raw vehicle audit log               |

---

## Project layout

```
.
├── main.go                    MQTT loop · provider detection · InfluxDB3 writer
│                              GetDevicesMap (loads + reloads devices.json)
├── devices.json               Runtime device registry (model/devEui/calibrations)
├── record/
│   └── record.go              SensorDataRecord · SensorTypes singleton (ST)
└── go-parse/
    ├── providers/
    │   ├── chirpstack/
    │   │   └── chirpstack.go  ChirpStack v4 LNS frame parser → LNSFrame
    │   └── everynet/
    │       └── everynet.go    Everynet LNS frame parser → LNSFrame
    └── devices/
        ├── registry/
        │   └── registry.go    Central model+submodel routing → ParseCustom / DecodeLNS
        ├── milesight/
        │   ├── milesight.go   ParseMilesightTLV · Decode(model, submodel, ...)
        │   ├── em300_di.go    EM300-DI decoder
        │   ├── em500_swl.go   EM500-SWL decoder
        │   └── ws101.go       WS101 smart button decoder
        ├── kron/
        │   ├── kron.go        Parse(model, submodel, ...) · Decode(model, submodel, ...)
        │   ├── ks3000_lora.go KS3000 LoRa binary decoder
        │   └── ks3000_wifi.go KS3000 WiFi JSON decoder
        └── khomp/
            ├── khomp.go       Decode(model, submodel, ...)
            ├── dtl200.go      DTL200 4-20 mA / 0-30 V decoder
            └── nit21li_emw104.go  NIT21LI + EMW104 weather station decoder
```

---

## Decoder file and routing convention

Each file owns exactly one device codec. Naming pattern: **`model_submodel.go`**
(lowercase, underscore-separated). When a model has no submodel (e.g. `DTL`,
`WS101`), the file name is `model.go`.

| File                      | Registry key       | Vendor map key     | Public API            |
| ------------------------- | ------------------ | ------------------ | --------------------- |
| `khomp/dtl200.go`         | `"DTL"`            | `"DTL"`            | `DecodeDTL200`        |
| `khomp/nit21li_emw104.go` | `"NIT21LI_EMW104"` | `"NIT21LI_EMW104"` | `DecodeNIT21LIEMW104` |
| `kron/ks3000_lora.go`     | `"KS3000_LORA"`    | `"KS3000_LORA"`    | `DecodeKS3000LORA`    |
| `kron/ks3000_wifi.go`     | `"KS3000_WIFI"`    | `"KS3000_WIFI"`    | `ParseKS3000WiFi`     |
| `milesight/em300_di.go`   | `"EM300_DI"`       | `"EM300_DI"`       | `DecodeEM300DI`       |
| `milesight/em500_swl.go`  | `"EM500_SWL"`      | `"EM500_SWL"`      | `DecodeEM500SWL`      |
| `milesight/ws101.go`      | `"WS101"`          | `"WS101"`          | `DecodeWS101`         |

**Adding a new device** (example: EM500-SMTC):

1. Create `go-parse/devices/milesight/em500_smtc.go` — implement `decodeEM500SMTC` and export `DecodeEM500SMTC`
2. Add `"EM500_SMTC": decodeEM500SMTC` to `modelDecoders` in `milesight.go`
3. Add `"EM500_SMTC": ...milesight.Decode...` to `lnsParsers` in `registry/registry.go`
4. Add device entries with `"deviceModel": "EM500_SMTC"` in `devices.json`

**Rules:**

- One file = one device codec. No internal submodel dispatch maps.
- Registry key = `MODEL_SUBMODEL` (uppercase). Fallback to `MODEL` handled by `lookupKeys`.
- Use underscore for transport differences on the same hardware (`ks3000_lora` / `ks3000_wifi`).
- Do not create placeholder files for unimplemented devices.

---

## Decode chain (end to end)

```
devices.json
  → GetDevicesMap()  →  deviceMap[deviceID] = DeviceConfig{Model, Submodel, ...}
                        devEUIToDeviceID[devEUI] = deviceID

MQTT message arrives on  device/<identifier>/telemetry
  → detectProvider()           → "chirpstackv4" | "everynet" | "custom"
  → resolveIncomingDevice()    → devEUI lookup → deviceID + DeviceConfig
  → chirpstack.Parse() / everynet.Parse()  → LNSFrame{Data(b64), Port, Timestamp}
  → registry.DecodeLNS(model, submodel, payload, ...)
      → lookupKeys() = ["MODEL_SUBMODEL", "MODEL"]   (submodel key tried first)
      → lnsParsers["MODEL_SUBMODEL"]  → vendor.Decode()
          → vendor binaryDecoders["MODEL_SUBMODEL"]  → decodeXxx()
              → []SensorDataRecord
  → writeSensorRecords()
      → device_model tag = lowercase(model + "_" + submodel)
      → sensor_data point per record → InfluxDB3 iot_sensors
```

All lookups are O(1) map operations. No type switches, no reflect.

---

## Naming convention for `sensor_type`

`sensor_type` names follow the pattern **`<domain>_<parameter>`** where
_parameter_ uses the standard domain abbreviation when one is established:

| rule                                           | example                                               |
| ---------------------------------------------- | ----------------------------------------------------- |
| Use standard abbreviation                      | `air_temp` (not `air_temperature`)                    |
| Use WMO/ISO symbol                             | `air_rh` (RH = relative humidity symbol)              |
| Drop suffix when root is unambiguous           | `air_press` (`press·ure`), `solar_rad` (`rad·iation`) |
| Keep full word when no shorter standard exists | `illuminance`, `wind_speed`, `water_level`            |

---

## Sensor type reference

All values are tag values for `sensor_type` in `sensor_data`. Defined in
`record.SensorTypes` (see `record/record.go`) — never as bare string literals.

### `nit21li_emw104` — weather station (Khomp NIT21LI + EMW104 expansion)

| sensor_type                | unit   | value type | description                                |
| -------------------------- | ------ | ---------- | ------------------------------------------ |
| `air_temp`                 | °C     | float      | Ambient air temperature (EMW104)           |
| `air_rh`                   | %      | float      | Ambient relative humidity (EMW104)         |
| `wind_speed`               | m/s    | float      | Average wind speed                         |
| `wind_gust`                | m/s    | float      | Peak wind speed (gust)                     |
| `wind_dir`                 | °      | float      | Wind direction (0° = North, clockwise)     |
| `rain_depth`               | mm     | float      | Accumulated rainfall depth                 |
| `solar_rad`                | W/m²   | float      | Global solar radiation                     |
| `illuminance`              | lux    | float      | Ambient light level                        |
| `uv_index`                 | —      | float      | UV radiation index                         |
| `air_press`                | hPa    | float      | Atmospheric pressure                       |
| `internal_temp`            | °C     | float      | Internal enclosure temperature             |
| `internal_rh`              | %      | float      | Internal relative humidity                 |
| `external_power`           | —      | bool       | `true` = external power; `false` = battery |
| `env_sensor_fail_status`   | —      | bool       | `true` = sensor failure detected           |
| `internal_battery_voltage` | V      | float      | Internal backup battery voltage            |
| `c1_state`                 | —      | bool       | Digital input 1 state                      |
| `c1_count`                 | pulses | int        | Cumulative pulse count from input 1        |
| `c2_state`                 | —      | bool       | Digital input 2 state                      |
| `c2_count`                 | pulses | int        | Cumulative pulse count from input 2        |

### `ks3000_lora` and `ks3000_wifi` — three-phase power meter (Kron KS3000)

Both variants produce identical `sensor_type` values, enabling unified queries
with `WHERE device_model LIKE 'ks3000%'`.

| sensor_type           | unit  | value type | description                       |
| --------------------- | ----- | ---------- | --------------------------------- |
| `voltage_u_ll_avg`    | V     | float      | Average line-to-line voltage (U0) |
| `voltage_u12/u23/u31` | V     | float      | Line voltages                     |
| `voltage_u1/u2/u3`    | V     | float      | Phase-to-neutral voltages         |
| `current_i_avg`       | A     | float      | Average current (I0)              |
| `current_in`          | A     | float      | Neutral current                   |
| `current_i1/i2/i3`    | A     | float      | Per-phase currents                |
| `frequency`           | Hz    | float      | Power system frequency            |
| `power_p_total`       | W     | float      | Total active power (P0)           |
| `power_p1/p2/p3`      | W     | float      | Per-phase active power            |
| `power_q_total`       | VAr   | float      | Total reactive power (Q0)         |
| `power_q1/q2/q3`      | VAr   | float      | Per-phase reactive power          |
| `power_s_total`       | VA    | float      | Total apparent power              |
| `power_s1/s2/s3`      | VA    | float      | Per-phase apparent power          |
| `power_factor`        | —     | float      | Three-phase power factor (FP0)    |
| `power_factor_1/2/3`  | —     | float      | Per-phase power factor            |
| `energy_a_plus`       | kWh   | float      | Active energy import (EA)         |
| `energy_q_plus`       | kVArh | float      | Reactive energy import (ER)       |
| `energy_a_minus`      | kWh   | float      | Active energy export (EAN)        |
| `energy_q_minus`      | kVArh | float      | Reactive energy export (ERN)      |
| `energy_s_total`      | kVAh  | float      | Apparent energy (ES)              |
| `horimetre`           | h     | float      | Hour meter                        |
| `air_temp`            | °C    | float      | Internal device temperature       |
| `error_code`          | —     | float      | Device error code (0 = no error)  |

### `em300_di` — digital input / pulse counter (Milesight EM300-DI)

| sensor_type     | unit   | value type | description                                       |
| --------------- | ------ | ---------- | ------------------------------------------------- |
| `battery_level` | %      | float      | Battery charge percentage                         |
| `air_temp`      | °C     | float      | Ambient temperature (if temperature probe fitted) |
| `air_rh`        | %      | float      | Ambient relative humidity (if probe fitted)       |
| `pulse_state`   | —      | bool       | GPIO digital input state                          |
| `pulse_counter` | pulses | int        | Cumulative pulse count                            |

### `em500_swl` — soil/water level (Milesight EM500-SWL)

| sensor_type     | unit | value type | description                                |
| --------------- | ---- | ---------- | ------------------------------------------ |
| `battery_level` | %    | float      | Battery charge percentage                  |
| `water_level`   | m    | float      | Water/liquid depth (0xFFFF = sensor fault) |

### `dtl` — analog input converter (Khomp DTL200, 4-20 mA / 0-30 V)

Probe mode (bytes[3]) determines the unit of `water_level`:
`0x00` → m depth · `0x01` → MPa pressure · `0x02` → Pa differential pressure

| sensor_type     | unit         | value type | description                             |
| --------------- | ------------ | ---------- | --------------------------------------- |
| `battery_level` | %            | float      | Battery level percentage                |
| `current_loop`  | mA           | float      | 4–20 mA current loop input              |
| `voltage_input` | V            | float      | 0–30 V analog voltage input             |
| `water_level`   | m / MPa / Pa | float      | Calculated value (probe-mode dependent) |

### `ws101` — smart button (Milesight WS101)

| sensor_type     | unit | value type | description                              |
| --------------- | ---- | ---------- | ---------------------------------------- |
| `battery_level` | %    | float      | Battery charge percentage                |
| `press_type`    | —    | int        | `1` single · `2` long · `3` double press |

---

## Provider detection

Detected from the MQTT message body — never from the topic path — so devices can
migrate between LoRa networks without reconfiguration.

| `provider` tag | format      | fingerprint                              |
| -------------- | ----------- | ---------------------------------------- |
| `chirpstackv4` | JSON object | root-level `deduplicationId` UUID        |
| `everynet`     | JSON object | `meta.device` + `params.payload` present |
| `custom`       | JSON array  | first element has `variable: "data"`     |

---

## Topic identifier resolution

The MQTT topic follows `device/<identifier>/telemetry`.

- For `custom`, `<identifier>` is `device_id` directly.
- For `chirpstackv4` / `everynet`, `<identifier>` is `dev_eui`, resolved to
  canonical `device_id` via the device registry.

---

## Device registry (`devices.json`)

Loaded at startup, reloaded every `DEVICE_REGISTRY_REFRESH_SEC` seconds (default 30)
without restarting the process. If the new file fails validation the previous registry
stays active and a warning is logged — the service never crashes on a bad hot-reload.

### Field reference

| field                 | type                  | required                  | notes                                                 |
| --------------------- | --------------------- | ------------------------- | ----------------------------------------------------- |
| `version`             | string                | —                         | top-level; log-only; bump when schema changes         |
| `device_id`           | UUIDv7                | **mandatory**             | unique device identifier                              |
| `device_model`        | string                | **mandatory**             | merged model+submodel key, e.g. `"em500_swl"`         |
| `device_type`         | `"lorawan"` \| `"ip"` | **mandatory**             | determines which hardware id is required              |
| `serial_number`       | string                | **mandatory**             | physical serial; use `""` if unknown (warning logged) |
| `asset_id`            | UUIDv7                | **mandatory**             | linked physical asset                                 |
| `dev_eui`             | 16 hex chars          | **mandatory** for lorawan | EUI-64 identifier                                     |
| `mac_address`         | MAC address           | **mandatory** for ip      | any format accepted by `net.ParseMAC`                 |
| `battery_voltage_max` | float                 | optional                  | V, defaults to 4.2 (LiPo/Li-Ion)                      |
| `battery_voltage_min` | float                 | optional                  | V, defaults to 3.3 (LoRa safe minimum)                |
| `probe_range_m`       | float                 | optional                  | DTL series: full-scale probe range in metres          |
| `asset_coords`        | object                | optional                  | `lat` [-90,90], `lng` [-180,180], `alt` (m)           |
| `calibrations`        | array                 | optional                  | per-sensor scale/offset/power corrections             |
| `allow`               | bool                  | optional                  | set `false` to disable without removing the entry     |

### Full example with all options

```json
{
  "version": "1",
  "devices": [
    {
      "device_id": "019b9ae2-fc84-7396-9ea8-fd2a041b7664",
      "device_model": "em500_swl",
      "device_type": "lorawan",
      "serial_number": "SN-EM500-001",
      "dev_eui": "24e124126d284622",
      "asset_id": "019df4d6-f351-77a5-a3c2-926523251c47",
      "asset_coords": { "lat": -23.565, "lng": -46.655, "alt": 760 },
      "battery_voltage_max": 4.2,
      "battery_voltage_min": 3.3,
      "calibrations": [
        {
          "sensor_type": "water_level",
          "scale": 0.01,
          "offset": 0.0,
          "power": 1.0
        }
      ]
    },
    {
      "device_id": "019b08df-26e7-7506-a5f6-916b2bef24f4",
      "device_model": "ks3000_wifi",
      "device_type": "ip",
      "serial_number": "SN-KS3000-001",
      "mac_address": "aa:bb:cc:dd:ee:ff",
      "asset_id": "019df4d6-f351-77be-901c-0aa0e4dadce9",
      "asset_coords": { "lat": -23.565, "lng": -46.655, "alt": 760 }
    },
    {
      "allow": false,
      "device_id": "019be702-190b-78be-ad3f-599935f7745c",
      "device_model": "ks3000_lora",
      "device_type": "lorawan",
      "serial_number": "",
      "dev_eui": "303331395230870e",
      "asset_id": "019df4d6-f351-77b0-b279-2c510f404ca4"
    }
  ]
}
```

**Validation rules (applied on every load):**

- `device_id` and `asset_id` must be valid UUIDv7 (`xxxxxxxx-xxxx-7xxx-...`)
- `device_type` must be `"lorawan"` or `"ip"` (case-insensitive)
- `dev_eui` must be present and 16 hex chars for `lorawan` devices
- `mac_address` must be a valid MAC address for `ip` devices
- `asset_coords.lat` must be in [-90, 90]; `lng` in [-180, 180]
- Duplicate `device_id` or `dev_eui` causes the whole file to be rejected
- Missing `serial_number` field (key absent, not empty) is a hard error; empty value is a warning

**Calibration formula:** `calibrated = (raw ^ power) × scale + offset`

Identity calibration (`scale=1, offset=0, power=1`) can be omitted entirely.

---

## Environment variables

| variable                      | default                        | description                                        |
| ----------------------------- | ------------------------------ | -------------------------------------------------- |
| `MQTT_BROKER`                 | _(required)_                   | e.g. `tcp://broker:1883`                           |
| `INFLUXDB_HOST`               | `http://influxdb.maua.br:8181` | InfluxDB3 Core URL                                 |
| `INFLUXDB_TOKEN`              | _(empty)_                      | Auth token (may be empty for unauthenticated Core) |
| `DEVICE_REGISTRY_FILE`        | `devices.json`                 | Path to the JSON device registry                   |
| `DEVICE_REGISTRY_REFRESH_SEC` | `30`                           | Poll interval (seconds) for hot-reload of registry |
