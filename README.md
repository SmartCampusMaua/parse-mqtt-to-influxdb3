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

| `device_model` in devices.json | `device_model` tag in InfluxDB3 |
| ------------------------------- | -------------------------------- |
| `"dtl200"`                      | `dtl200`                         |
| `"nit21li_emw104"`              | `nit21li_emw104`                 |
| `"ks3000_lora"`                 | `ks3000_lora`                    |
| `"ks3000_wifi"`                 | `ks3000_wifi`                    |
| `"em300_di"`                    | `em300_di`                       |
| `"em500_swl"`                   | `em500_swl`                      |
| `"em500_smtc"`                  | `em500_smtc`                     |
| `"ws101"`                       | `ws101`                          |
| `"vs373"`                       | `vs373`                          |
| `"at101"`                       | `at101`                          |
| `"lnv3_sm3dl"`                  | `lnv3_sm3dl`                     |
| `"lnv3_svc"`                    | `lnv3_svc`                       |

This single merged tag eliminates a redundant index dimension. Queries are
`WHERE device_model = 'em300_di'`; to match all EM500 variants use
`WHERE device_model LIKE 'em500%'`.

`*` `dev_eui` is written only for LoRaWAN providers (`chirpstackv4`, `everynet`).

### `raw` — audit measurement in `audit_iot` database

| element     | value                                          |
| ----------- | ----------------------------------------------- |
| measurement | `raw`                                          |
| tags        | `device_id`, `event_type = "payload_ingest"`   |
| fields      | `raw_data` (string ≤ 512 chars)                |
| timestamp   | ingestion time (time.Now() at message receipt) |

### `sensor_calibration` — written once at startup

| element     | value                                              |
| ----------- | --------------------------------------------------- |
| measurement | `sensor_calibration`                               |
| tags        | `device_id`, `sensor_type`                         |
| fields      | `scale` (float), `offset` (float), `power` (float) |

Formula applied by consumers at query time: `calibrated = (raw ^ power) × scale + offset`

This exists specifically for values that are a **cloud-side calibration
curve** applied to a **raw device reading**, not a device measurement in its
own right — e.g. `lnv3_sm3dl`'s `smdl1/2/3` (raw ADC counts from a soil probe;
the moisture-% curve is per-probe and can be recalibrated without touching
historical data) or a water-meter's pulse-to-volume ratio. Decoders write the
raw value to `sensor_data` and the transform to `sensor_calibration` — never
apply the transform in Go and throw away the raw value, because that bakes a
possibly-wrong calibration into history permanently.

---

## Databases

Four InfluxDB3 databases are initialised simultaneously on the same host and token:

| database            | content                             |
| -------------------- | ------------------------------------ |
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
│                              GetAssetsMap (loads + reloads assets.json)
├── devices.json               Runtime device registry (model/devEui/calibrations)
├── assets.json                Runtime asset registry (asset_id/coords/device_ids)
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
        │   ├── em500_smtc.go  EM500-SMTC decoder
        │   ├── ws101.go       WS101 smart button decoder
        │   ├── vs373.go       VS373 bed/room presence & vital signs decoder
        │   └── at101.go       AT101 GPS/WiFi asset tracker decoder
        ├── imt/
        │   ├── imt.go         parseIMT (tag-based protocol, not TLV) · Decode(model, submodel, ...)
        │   ├── lnv3_sm3dl.go  LoraNodeV3 soil moisture (3 depth levels) decoder
        │   └── lnv3_svc.go    LoraNodeV3 solenoid valve control decoder
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
(lowercase, underscore-separated). When a model has no submodel (e.g.
`DTL200`, `WS101`, `VS373`, `AT101`), the file name is `model.go`.

| File                        | Registry key       | Vendor map key     | Public API            |
| ---------------------------- | ------------------- | ------------------- | ----------------------- |
| `khomp/dtl200.go`            | `"DTL200"`          | `"DTL200"`          | `DecodeDTL200`         |
| `khomp/nit21li_emw104.go`    | `"NIT21LI_EMW104"`  | `"NIT21LI_EMW104"`  | `DecodeNIT21LIEMW104`  |
| `kron/ks3000_lora.go`        | `"KS3000_LORA"`     | `"KS3000_LORA"`     | `DecodeKS3000LORA`     |
| `kron/ks3000_wifi.go`        | `"KS3000_WIFI"`     | `"KS3000_WIFI"`     | `ParseKS3000WiFi`      |
| `milesight/em300_di.go`      | `"EM300_DI"`        | `"EM300_DI"`        | `DecodeEM300DI`        |
| `milesight/em500_swl.go`     | `"EM500_SWL"`       | `"EM500_SWL"`       | `DecodeEM500SWL`       |
| `milesight/em500_smtc.go`    | `"EM500_SMTC"`      | `"EM500_SMTC"`      | `DecodeEM500SMTC`      |
| `milesight/ws101.go`         | `"WS101"`           | `"WS101"`           | `DecodeWS101`          |
| `milesight/vs373.go`         | `"VS373"`           | `"VS373"`           | `DecodeVS373`          |
| `milesight/at101.go`         | `"AT101"`           | `"AT101"`           | `DecodeAT101`          |
| `imt/lnv3_sm3dl.go`          | `"LNV3_SM3DL"`      | `"LNV3_SM3DL"`      | `DecodeLNV3SM3DL`      |
| `imt/lnv3_svc.go`            | `"LNV3_SVC"`        | `"LNV3_SVC"`        | `DecodeLNV3SVC`        |

**Adding a new device** (example: a new Milesight TLV-protocol device `EM310-TILT`):

1. Create `go-parse/devices/milesight/em310_tilt.go` — implement `decodeEM310TILT` and export `DecodeEM310TILT`
2. Add `"EM310_TILT": decodeEM310TILT` to `modelDecoders` in `milesight.go`; if it introduces new
   `(channel, type)` byte pairs, add their lengths to `genericTypeLengths` / `channelTypeOverride`
3. Add `"EM310_TILT": ...milesight.Decode...` to `lnsParsers` in `registry/registry.go`
4. Add device entries with `"device_model": "em310_tilt"` in `devices.json`
5. In `record.go`: reuse an existing `SensorTypes` field where the physical
   quantity and unit already match (e.g. `battery_level`); add a new field
   only when nothing fits. Follow the [naming convention](#naming-convention-for-sensor_type) below.

**Rules:**

- One file = one device codec. No internal submodel dispatch maps.
- Registry key = `MODEL_SUBMODEL` (uppercase). Fallback to `MODEL` handled by `lookupKeys`.
- Use underscore for transport differences on the same hardware (`ks3000_lora` / `ks3000_wifi`).
- Do not create placeholder files for unimplemented devices.
- Not every vendor uses a TLV wire format — `imt/imt.go` decodes a flat
  sequential-tag protocol instead of `[channel][type][data]` TLVs. The
  per-device decoder files still follow the same one-file-per-codec shape;
  only the shared parser underneath differs.
- Prefer emitting the **raw** device value and pushing any curve-fit/scale
  correction into `sensor_calibration` (see above) over hardcoding a
  correction constant in Go, whenever that correction is something that
  could reasonably be recalibrated later without a code change.

---

## Decode chain (end to end)

```
devices.json              assets.json
  → GetDevicesMap()          → GetAssetsMap()
    deviceMap[deviceID] =      deviceID -> AssetConfig{AssetID, AssetCoords}
    DeviceConfig{Model, ...}     (resolved from each asset's device_ids array)
    devEUIToDeviceID[devEUI]
                    ↓                    ↓
              mergeAssetInfo() backfills AssetID/AssetCoords onto DeviceConfig

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

`assets.json` is decoupled from `devices.json` on purpose: `AssetConfig` is
resolved separately and only merged into each `DeviceConfig` in memory. No
decoder currently reads `AssetID`/`AssetCoords` — it's carried on
`DeviceConfig` for future consumers (e.g. a geo-tagging writer). Because
nothing downstream depends on it, a bad/missing `assets.json` logs a warning
and starts with zero asset links rather than crashing the service, unlike a
bad `devices.json`, which is fatal (see [Asset registry](#asset-registry-assetsjson) below).

---

## Naming convention for `sensor_type`

`sensor_type` names follow the pattern **`<domain>_<parameter>`** where
_parameter_ uses the standard domain abbreviation when one is established:

| rule                                                     | example                                                       |
| ---------------------------------------------------------- | ---------------------------------------------------------------- |
| Use standard abbreviation                                 | `air_temp` (not `air_temperature`)                             |
| Use WMO/ISO symbol                                        | `air_rh` (RH = relative humidity symbol)                       |
| Drop suffix when root is unambiguous                      | `air_press` (`press·ure`), `solar_rad` (`rad·iation`)          |
| Keep full word when no shorter standard exists            | `illuminance`, `wind_speed`, `water_level`                     |
| Enumerated/indexed components: digit directly on the base, **no underscore before it** | `sv1`/`sv2`/`sv3`, `smdl1`/`smdl2`/`smdl3`, `voltage_u1`, `power_p1`, `region1_occupancy` |

The indexed-component rule is a single sitewide convention, not
per-device — verified by auditing every existing indexed family
(`voltage_u1/2/3/12/23/31`, `current_i1/2/3`, `power_p1/2/3`, `power_q1/2/3`,
`power_s1/2/3`, `c1_state`/`c2_state` all already used no underscore).
`power_factor1/2/3` and `digital_input1/2` were the only two exceptions
found (they used `power_factor_1`/`digital_input_1`); since neither was in
live use yet, both were normalized rather than adding a third variant.

`sensor_type` values are reused **across devices** when the physical
quantity and unit genuinely match — `battery_level` and `temperature_alarm`
each appear on more than one device family below. `device_model` is what
disambiguates a reused tag's exact semantics when they differ slightly
(e.g. `temperature_alarm`'s enum codes differ between `em500_smtc` and
`at101` — see the table for each). `electrical_conductivity` is currently
emitted by `em500_smtc` only, despite `record.go`'s field comment grouping
it with `em500_swl` — that grouping describes the shared "Water / soil"
struct section, not actual reuse; `em500_swl.go` never writes it.

Ten `SensorTypes` fields exist in `record.go` but are currently **not
emitted by any decoder** — grep `st.<Field>` across `go-parse/devices/` to
re-verify before relying on any of these:

- `water_flow`, `water_conv`, `pulse_conv` — reserved for EM300-DI v1.3+
  firmware's raw water-conversion channel, which is parsed but only used
  internally to compute `pulse_count`; the conversion factors themselves
  aren't written out today.
- `digital_input1`, `digital_input2`, `interrupt_level`, `interrupt_status`
  — standardised DTL200-SWL I/O names, not emitted by `dtl200.go`; reserved
  for a probe variant that reads those pins.
- `battery_voltage` — distinct from `battery_level` (%); no decoder emits a
  raw battery voltage today (Khomp NIT21LI emits `internal_battery_voltage`
  instead, a different field).
- `press_state`, `press_count` — `ws101.go` only emits `battery_level` and
  `press_type`; these two WS101-shaped fields are unused.

---

## Sensor type reference

All values are tag values for `sensor_type` in `sensor_data`. Defined in
`record.SensorTypes` (see `record/record.go`) — never as bare string literals.

### `nit21li_emw104` — weather station (Khomp NIT21LI + EMW104 expansion)

| sensor_type                | unit   | value type | description                                |
| ---------------------------- | -------- | ------------ | --------------------------------------------- |
| `air_temp`                 | °C     | float      | Ambient air temperature (EMW104)           |
| `air_rh`                   | %      | float      | Ambient relative humidity (EMW104)         |
| `wind_speed`                | m/s    | float      | Average wind speed                         |
| `wind_gust`                 | m/s    | float      | Peak wind speed (gust)                     |
| `wind_dir`                  | °      | float      | Wind direction (0° = North, clockwise)     |
| `rain_depth`                | mm     | float      | Accumulated rainfall depth                 |
| `solar_rad`                 | W/m²   | float      | Global solar radiation                     |
| `illuminance`               | lux    | float      | Ambient light level                        |
| `uv_index`                  | —      | float      | UV radiation index                         |
| `air_press`                 | hPa    | float      | Atmospheric pressure                       |
| `internal_temp`             | °C     | float      | Internal enclosure temperature             |
| `internal_rh`               | %      | float      | Internal relative humidity                 |
| `external_power`            | —      | bool       | `true` = external power; `false` = battery |
| `env_sensor_fail_status`    | —      | bool       | `true` = sensor failure detected           |
| `internal_battery_voltage`  | V      | float      | Internal backup battery voltage            |
| `c1_state`                  | —      | bool       | Digital input 1 state                      |
| `c1_count`                  | pulses | int        | Cumulative pulse count from input 1        |
| `c2_state`                  | —      | bool       | Digital input 2 state                      |
| `c2_count`                  | pulses | int        | Cumulative pulse count from input 2        |

This device has no dedicated `battery_level` (%) reading — `internal_battery_voltage`
is the field the decoder emits for battery status. Every field above is only
emitted when its corresponding bit is set in the payload's sensor mask
(device sends deltas, not a fixed frame).

### `ks3000_lora` — three-phase power meter (Kron KS3000, LoRaWAN binary)

Full per-phase breakdown, ID-mapped straight from the wire format.

| sensor_type           | unit  | value type | description                       |
| ------------------------ | ------- | ------------ | ------------------------------------ |
| `voltage_u_ll_avg`    | V     | float      | Average line-to-line voltage (U0) |
| `voltage_u12/u23/u31` | V     | float      | Line-to-line voltages             |
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
| `power_factor1/2/3`   | —     | float      | Per-phase power factor            |
| `energy_a_plus`       | kWh   | float      | Active energy import (EA)         |
| `energy_q_plus`       | kVArh | float      | Reactive energy import (ER)       |
| `energy_a_minus`      | kWh   | float      | Active energy export (EAN)        |
| `energy_q_minus`      | kVArh | float      | Reactive energy export (ERN)      |
| `energy_s_total`      | kVAh  | float      | Apparent energy (ES)              |
| `horimetre`           | h     | float      | Hour meter                        |
| `air_temp`            | °C    | float      | Internal device temperature       |
| `error_code`          | —     | float      | Device error code (0 = no error)  |

### `ks3000_wifi` — three-phase power meter (Kron KS3000, WiFi JSON)

**Not identical to `ks3000_lora`** — the WiFi JSON payload only carries the
aggregate fields, not the per-phase breakdown. Use `ks3000_lora` if you need
per-phase voltage/current/power.

| sensor_type        | unit  | value type | description             |
| --------------------- | ------- | ------------ | -------------------------- |
| `voltage_u_ll_avg` | V     | float      | Average line-to-line voltage (U0) |
| `current_i_avg`    | A     | float      | Average current (I0)    |
| `frequency`        | Hz    | float      | Power system frequency  |
| `power_p_total`    | W     | float      | Total active power (P0) |
| `power_q_total`    | VAr   | float      | Total reactive power (Q0) |
| `power_factor`     | —     | float      | Three-phase power factor (FP0) |
| `energy_a_plus`    | kWh   | float      | Active energy import (EA) |
| `energy_q_plus`    | kVArh | float      | Reactive energy import (ER) |
| `energy_a_minus`   | kWh   | float      | Active energy export (EAN) |
| `energy_q_minus`   | kVArh | float      | Reactive energy export (ERN) |
| `error_code`       | —     | float      | Device error code (CE, 0 = no error) |

Every field except `error_code` is only written when the JSON payload's
value is non-zero (`buildRecords` in `kron/kron.go` skips zero fields for
those — a genuine `0` reading is indistinguishable from "not reported" for
this device). `error_code` is the one exception: it's always written,
including `0`, since `0` is itself meaningful ("no error") rather than
ambiguous with "not reported."

### `em300_di` — digital input / pulse counter (Milesight EM300-DI)

| sensor_type     | unit   | value type | description                                       |
| ------------------ | -------- | ------------ | ------------------------------------------------------ |
| `battery_level` | %      | float      | Battery charge percentage                         |
| `air_temp`      | °C     | float      | Ambient temperature (if temperature probe fitted) |
| `air_rh`        | %      | float      | Ambient relative humidity (if probe fitted)       |
| `pulse_state`   | —      | bool       | GPIO digital input state                          |
| `pulse_count`   | pulses | int        | Cumulative pulse count (raw counter, or computed from the v1.3+ water-conversion channel) |

### `em500_swl` — soil/water level (Milesight EM500-SWL)

| sensor_type     | unit | value type | description                                |
| ------------------ | ------ | ------------ | --------------------------------------------- |
| `battery_level` | %    | float      | Battery charge percentage                  |
| `water_level`   | m    | float      | Water/liquid depth (0xFFFF/0xFFFD sensor fault codes are logged, not written) |

### `em500_smtc` — soil moisture/temperature/conductivity probe (Milesight EM500-SMTC)

| sensor_type            | unit    | value type | description                                                 |
| -------------------------- | --------- | ------------ | ----------------------------------------------------------------- |
| `battery_level`        | %       | float      | Battery charge percentage                                   |
| `soil_temp`             | °C      | float      | Soil probe temperature (plain channel or the temp+alarm channel) |
| `soil_moisture`        | %       | float      | Soil moisture (old ÷2 or new ÷100 resolution channel, unified) |
| `electrical_conductivity` | µS/cm   | int        | Raw electrical conductivity reading                          |
| `temperature_mutation` | °C      | float      | Temperature delta reported alongside a mutation alarm       |
| `temperature_alarm`    | —       | int (enum) | `0` release · `1` threshold · `2` mutation                  |

Sensor-fault sentinels (`0xFFFF`/`0xFFFD`) on temperature/moisture/EC
channels are logged and the point is skipped, matching `em500_swl`.

### `dtl200` — analog input converter (Khomp DTL200, 4-20 mA / 0-30 V)

Probe mode (payload byte 2) determines the unit of `water_level`:
`0x00` → m depth · `0x01` → MPa pressure · `0x02` → Pa differential pressure

`decodeDTL200` returns no records at all for LoRaWAN fPort 5 or 7 — the
only port-based branch in any decoder in this codebase (`khomp/dtl200.go`).
Those ports carry something other than the sensor frame this decoder
expects (likely downlink acks); nothing is logged when this triggers.

| sensor_type     | unit         | value type | description                             |
| ------------------ | -------------- | ------------ | ------------------------------------------ |
| `battery_level` | %            | float      | Battery level percentage                |
| `current_loop`  | mA           | float      | 4–20 mA current loop input              |
| `voltage_input` | V            | float      | 0–30 V analog voltage input             |
| `water_level`   | m / MPa / Pa | float      | Calculated value (probe-mode dependent), only written for probe modes 0x00-0x02 |

`digital_input1/2`, `interrupt_level`, `interrupt_status` are defined as
standardised DTL200-SWL I/O names in `record.go` but are not currently
emitted by `dtl200.go` — reserved for a probe variant that reads those pins.

### `ws101` — smart button (Milesight WS101)

| sensor_type     | unit | value type | description                              |
| ------------------ | ------ | ------------ | -------------------------------------------- |
| `battery_level` | %    | float      | Battery charge percentage                |
| `press_type`    | —    | int        | `1` single · `2` long · `3` double press |

### `vs373` — bed/room presence & vital signs (Milesight VS373)

No battery — VS373 is a mains-powered ceiling sensor and doesn't report one.
Supports **both** firmware generations (v1.0.1 and v1.0.2); the decoder
unifies both onto the same `sensor_type` set below.

| sensor_type                    | unit  | value type | description                                                                                  |
| --------------------------------- | ------- | ------------ | ------------------------------------------------------------------------------------------------ |
| `detection_status`              | —     | int (enum) | `0` normal · `1` vacant · `2` in_bed · `3` out_of_bed · `4` fall                              |
| `target_status`                 | —     | int (enum) | `0` normal · `1` motionless · `2` abnormal · `3` lying_down                                   |
| `use_time_now`                  | s     | int        | Duration of current status                                                                    |
| `use_time_today`                | s     | int        | Cumulative duration today                                                                     |
| `region1..6_occupancy`          | —     | bool       | Per-region occupied/vacant (up to 4 regions on v1.0.1, up to 6 on v1.0.2)                     |
| `region1..6_out_of_bed_time`    | s     | int        | Per-region out-of-bed duration                                                                |
| `respiratory_status`            | —     | int (enum) | `1` no_data_input · `2` normal · `3` tachypnea · `4` bradypnea · `5` undetectable              |
| `respiratory_rate`              | breaths/min | float | Breathing rate                                                                                 |
| `alarm_id`                      | —     | int        | Alarm event identifier                                                                         |
| `alarm_type`                    | —     | int (enum) | `0` fall · `1` motionless · `2` dwell · `3` out_of_bed · `4` occupied · `5` vacant · `6` bradynea · `7` tachypnea · `8` lying_down |
| `alarm_status`                  | —     | int (enum) | `1` triggered · `2` deactivated · `3` ignored · `4` respiratory_status                        |
| `alarm_region_id`               | —     | int        | Only present when `alarm_type` is `out_of_bed`(3)/`bradynea`(6)/`tachypnea`(7)                |

v1.0.1's region-occupancy raw encoding is bit-inverted relative to v1.0.2
(`0`=occupied on v1.0.1 vs bit=`1`=occupied on v1.0.2) — the decoder
normalizes both to the same boolean meaning before writing.

WiFi/BLE-style region-type configuration (`0x09/0xB2`) is tokenized (its
length is registered in `channelTypeOverride`) but intentionally **not**
written to `sensor_data` — it's static configuration, not a measurement.

History/backfill records (`0x20/0xCE`) are a real known gap, not a safely
handled one: `channelTypeOverride` registers only **one** length for that
`(channel, type)` pair — EM500-SWL's 6 bytes — and `ParseMilesightTLV` has
no model context when tokenizing (it runs before the model is looked up).
VS373 (9B), EM500-SMTC (10B), and AT101 (12B) all reuse the same pair with
different, incompatible lengths. If any of those three ever actually sends
a `0x20/0xCE` TLV, it will be mis-consumed as 6 bytes, corrupting the parse
of whatever follows it in that uplink — not a clean "unknown channel,
stop." No production payload containing this TLV has been observed from
these three devices yet, which is the only reason this hasn't surfaced; see
the comment in `milesight.go` before enabling history/backfill for any of
them.

### `at101` — GPS/WiFi asset tracker (Milesight AT101)

| sensor_type        | unit | value type | description                                                        |
| ---------------------- | ------ | ------------ | ---------------------------------------------------------------------- |
| `battery_level`     | %    | float      | Battery charge percentage                                          |
| `temperature`       | °C   | float      | Ambient temperature (plain channel or the temp+alarm channel)      |
| `temperature_alarm` | —    | int (enum) | `0` normal · `1` abnormal                                          |
| `latitude`          | °    | float      | GPS/WiFi-resolved latitude (from normal `0x04` or alarm/geofence `0x84` report) |
| `longitude`         | °    | float      | GPS/WiFi-resolved longitude                                        |
| `motion_status`     | —    | int (enum) | `0` unknown · `1` start · `2` moving · `3` stop                     |
| `geofence_status`   | —    | int (enum) | `0` inside · `1` outside · `2` unset · `3` unknown                  |
| `device_position`   | —    | int (enum) | `0` normal · `1` tilt                                              |
| `tamper_status`     | —    | int (enum) | `0` install · `1` uninstall                                        |

WiFi scan results (`0x06/0xD9`) are tokenized but not decoded — each result
carries a MAC address (not representable as float/int/bool) and a single
uplink can report several, which doesn't fit the one-value-per-sensor-type-
per-timestamp model this codebase uses everywhere else. History/backfill
records (`0x20/0xCE`, 12B here) have the same unresolved shared-length risk
described in the VS373 section above.

### `lnv3_sm3dl` — soil moisture, 3 depth levels (IMT LoraNodeV3-SM3DL)

Uses IMT's tag-based protocol (`imt/imt.go`), not Milesight TLV.

| sensor_type     | unit | value type | description                                                    |
| ------------------ | ------ | ------------ | ------------------------------------------------------------------ |
| `smdl1`         | raw  | int        | Raw ADC reading, probe depth 1 (nominally 10 cm)                |
| `smdl2`         | raw  | int        | Raw ADC reading, probe depth 2 (nominally 30 cm)                |
| `smdl3`         | raw  | int        | Raw ADC reading, probe depth 3 (nominally 70 cm)                |
| `board_voltage` | V    | float      | Board supply voltage                                            |

`smdl1/2/3` are intentionally **raw**, not a computed moisture percentage —
the conversion (`15000 × raw^power`, scaled per depth) is a per-probe
calibration curve stored in `sensor_calibration` via each device's
`calibrations` entry in `devices.json` (`power=-0.8`, `scale` folded from
the depth-specific divisor), not hardcoded in the decoder. See the
`sensor_calibration` section above for why.

### `lnv3_svc` — solenoid valve control (IMT LoraNodeV3-SVC)

| sensor_type     | unit | value type | description                                      |
| ------------------ | ------ | ------------ | ----------------------------------------------------- |
| `sv1`           | raw  | int        | Raw ADC reading, solenoid valve channel 1             |
| `sv2`           | raw  | int        | Raw ADC reading, solenoid valve channel 2             |
| `sv3`           | raw  | int        | Raw ADC reading, solenoid valve channel 3             |
| `pulse_count`   | —    | int        | Solenoid actuation counter (reuses the same sensor_type as EM300-DI's pulse counter) |
| `board_voltage` | V    | float      | Board supply voltage                                  |

`sv1/2/3` are raw, like `lnv3_sm3dl`'s `smdl1/2/3` — the open/closed
decision is a threshold applied downstream via `sensor_calibration`
(`offset=-1500`, `scale=1`, `power=1`; `calibrated = raw - 1500`, open when
`calibrated > 0`), so the threshold can be recalibrated without reprocessing
history.

---

## Provider detection

Detected from the MQTT message body — never from the topic path — so devices can
migrate between LoRa networks without reconfiguration.

| `provider` tag | format      | fingerprint                              |
| ---------------- | ------------- | ------------------------------------------- |
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

A bad **initial** load (at startup) is fatal — `devices.json` is essential
routing data.

Asset assignment (`asset_id`, geolocation) is **not** part of this file — see
[Asset registry (`assets.json`)](#asset-registry-assetsjson) below.

### Field reference

| field                 | type                  | required                  | notes                                                 |
| ----------------------- | ----------------------- | ---------------------------- | ---------------------------------------------------------- |
| `version`             | string                | —                         | top-level; log-only; bump when schema changes         |
| `device_id`           | UUIDv7                | **mandatory**             | unique device identifier                              |
| `device_model`        | string                | **mandatory**             | merged model+submodel key, e.g. `"em500_swl"`         |
| `device_type`         | `"lorawan"` \| `"ip"` | **mandatory**             | determines which hardware id is required              |
| `serial_number`       | string                | **mandatory**             | physical serial; use `""` if unknown (warning logged) |
| `dev_eui`             | 16 hex chars          | **mandatory** for lorawan | EUI-64 identifier                                     |
| `mac_address`         | MAC address           | **mandatory** for ip      | any format accepted by `net.ParseMAC`                 |
| `battery_voltage_max` | float                 | optional                  | V, defaults to 4.2 (LiPo/Li-Ion)                      |
| `battery_voltage_min` | float                 | optional                  | V, defaults to 3.3 (LoRa safe minimum)                |
| `probe_range_m`       | float                 | optional                  | DTL series: full-scale probe range in metres          |
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
      "mac_address": "aa:bb:cc:dd:ee:ff"
    },
    {
      "allow": false,
      "device_id": "019be702-190b-78be-ad3f-599935f7745c",
      "device_model": "ks3000_lora",
      "device_type": "lorawan",
      "serial_number": "",
      "dev_eui": "303331395230870e"
    }
  ]
}
```

**Validation rules (applied on every load):**

- `device_id` must be a valid UUIDv7 (`xxxxxxxx-xxxx-7xxx-...`)
- `device_type` must be `"lorawan"` or `"ip"` (case-insensitive)
- `dev_eui` must be present and 16 hex chars for `lorawan` devices
- `mac_address` must be a valid MAC address for `ip` devices
- Duplicate `device_id` or `dev_eui` causes the whole file to be rejected
- Missing `serial_number` field (key absent, not empty) is a hard error; empty value is a warning

**Calibration formula:** `calibrated = (raw ^ power) × scale + offset`

Identity calibration (`scale=1, offset=0, power=1`) can be omitted entirely.

---

## Asset registry (`assets.json`)

Physical asset metadata (`asset_id`, geolocation) lives here, **decoupled**
from `devices.json` — a device is a piece of hardware with a model and
routing identity; an asset is the physical thing it's mounted on (a tree, a
tank, a vehicle, a room). One asset can carry multiple devices (e.g. a
`lnv3_sm3dl` soil probe and an `lnv3_svc` valve controller on the same
irrigation zone) via the asset's own `device_ids` array — the cross-reference
lives on the asset side, not duplicated on every device.

Loaded at startup and reloaded on the same `DEVICE_REGISTRY_REFRESH_SEC`
interval as `devices.json`. Unlike `devices.json`, a bad or missing
`assets.json` is **non-fatal**: it logs a warning and the service starts (or
keeps running, on a reload) with the last known-good asset links — no
decoder actually reads `AssetID`/`AssetCoords` today, so there's nothing to
crash.

### Field reference

| field                 | type    | required      | notes                                                   |
| ----------------------- | --------- | ---------------- | ------------------------------------------------------------ |
| `version`             | string  | —              | top-level; log-only; bump when schema changes           |
| `asset_id`            | UUIDv7  | **mandatory**  | unique asset identifier                                 |
| `asset_coords`        | object  | optional       | `lat` [-90,90], `lng` [-180,180], `alt` (m)              |
| `device_ids`          | array   | optional       | `device_id`s (from `devices.json`) mounted on this asset; each must be a valid UUIDv7 |

### Full example

```json
{
  "version": "1",
  "assets": [
    {
      "asset_id": "019df4d6-f351-77a5-a3c2-926523251c47",
      "asset_coords": { "lat": -23.565, "lng": -46.655, "alt": 760 },
      "device_ids": [
        "019b9ae2-fc84-7396-9ea8-fd2a041b7664",
        "019be6eb-b5bb-7fbf-ba3b-3101febc1129"
      ]
    }
  ]
}
```

**Validation rules (applied on every load):**

- `asset_id` must be a valid UUIDv7
- `asset_coords.lat` must be in [-90, 90]; `lng` in [-180, 180]
- Every entry in `device_ids` must be a valid UUIDv7
- Duplicate `asset_id` causes the whole file to be rejected
- A `device_id` listed under more than one asset is a soft warning (last one loaded wins), not a hard error
- A `device_id` in `assets.json` with no matching entry in `devices.json` is not an error — it's simply never resolved to a live `DeviceConfig`

---

## Environment variables

| variable                      | default                        | description                                        |
| -------------------------------- | --------------------------------- | ------------------------------------------------------- |
| `MQTT_BROKER`                 | _(required)_                   | e.g. `tcp://broker:1883`                           |
| `INFLUXDB_HOST`               | `http://influxdb.maua.br:8181` | InfluxDB3 Core URL                                 |
| `INFLUXDB_TOKEN`              | _(empty)_                      | Auth token (may be empty for unauthenticated Core) |
| `DEVICE_REGISTRY_FILE`        | `devices.json`                 | Path to the JSON device registry                   |
| `ASSET_REGISTRY_FILE`         | `assets.json`                  | Path to the JSON asset registry                    |
| `DEVICE_REGISTRY_REFRESH_SEC` | `30`                            | Poll interval (seconds) for hot-reload of both registries |
