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
| tags        | `sensor_type`, `device_model`, `device_id`, `provider`, `dev_eui`\*, `sensor_id`\*\* |
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
| `"uc100"`                       | `uc100`                          |
| `"uc300"`                       | `uc300`                          |
| `"uc501"`                       | `uc501`                          |
| `"uc511"`                       | `uc511`                          |
| `"vs370"`                       | `vs370`                          |

This single merged tag eliminates a redundant index dimension. Queries are
`WHERE device_model = 'em300_di'`; to match all EM500 variants use
`WHERE device_model LIKE 'em500%'`.

`*` `dev_eui` is written only for LoRaWAN providers (`chirpstackv4`, `everynet`).

`**` `sensor_id` is written only when the record resolves to an entry in
[`sensors.json`](#sensor-registry-sensorsjson) — every non-UC decoder resolves
it by `(device_id, sensor_type, device_index)`; UC100/UC300/UC501 resolve it
by Modbus/IO channel via
[`device_channels.json`](#device-channel-registry-device_channelsjson). Empty
when unregistered.

### `raw` — audit measurement in `audit_iot` database

| element     | value                                                    |
| ----------- | --------------------------------------------------------- |
| measurement | `raw`                                                    |
| tags        | `device_id`, `asset_id`\*, `event_type = "payload_ingest"` |
| fields      | `raw_data` (string ≤ 512 chars)                          |
| timestamp   | ingestion time (time.Now() at message receipt)           |

\* `asset_id` is a snapshot of whichever asset the device was assigned to at
that instant (see [assets.json](#asset-registry-assetsjson)) — omitted when
unassigned. `raw` is already a continuous, timestamped record of every
message, so the full assignment history is reconstructable from it without
any separate change-tracking.

`sensor_data` and `raw` always hold exactly the value transmitted by the
sensor, untouched — no calibration or unit-conversion happens anywhere in
this pipeline. That's a client-side/user-space concern applied by whatever
consumes the SmartCampusMaua API, not by this repo or the API server.

---

## Databases

Four InfluxDB3 databases are initialised simultaneously on the same host and token:

| database            | content                             |
| -------------------- | ------------------------------------ |
| `iot_sensors`       | `sensor_data`                       |
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
│                              GetSensorsMap (loads + reloads sensors.json)
│                              GetDeviceChannelsMap (loads + reloads device_channels.json)
│                              resolveChannelRecord (UC100/UC300/UC501 channel → sensor resolution)
├── devices.json               Runtime device registry (model/devEui)
├── assets.json                Runtime asset registry (asset_id/coords/device_ids)
├── sensors.json                Runtime sensor registry (sensor_id/sensor_type/device_index)
├── device_channels.json        Runtime UC-series Modbus/IO channel routing
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
        │   ├── at101.go       AT101 GPS/WiFi asset tracker decoder
        │   ├── uc.go          ParseUCTLV — shared UC100/UC300/UC501 tokenizer (Modbus is variable-length)
        │   ├── uc100.go       UC100 Modbus-only decoder
        │   ├── uc300.go       UC300 Modbus + fixed GPIO/PT100/ADC decoder (channel-routed, see below)
        │   ├── uc501.go       UC501/UC50x Modbus + site-wired GPIO/analog-input decoder (channel-routed)
        │   ├── uc511.go       UC511/UC512 irrigation valve + pipe-pressure decoder (fixed-purpose, no RS485)
        │   └── vs370.go       VS370 single-zone occupancy/illuminance presence sensor decoder
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
| `milesight/uc100.go`         | `"UC100"`           | `"UC100"`           | `DecodeUC100`          |
| `milesight/uc300.go`         | `"UC300"`           | `"UC300"`           | `DecodeUC300`          |
| `milesight/uc501.go`         | `"UC501"`           | `"UC501"`           | `DecodeUC501`          |
| `milesight/uc511.go`         | `"UC511"`           | `"UC511"`           | `DecodeUC511`          |
| `milesight/vs370.go`         | `"VS370"`           | `"VS370"`           | `DecodeVS370`          |

**Adding a new device** (example: a new Milesight TLV-protocol device `EM310-TILT`):

1. Create `go-parse/devices/milesight/em310_tilt.go` — implement `decodeEM310TILT` and export `DecodeEM310TILT`
2. Add `"EM310_TILT": decodeEM310TILT` to `modelDecoders` in `milesight.go`; if it introduces new
   `(channel, type)` byte pairs, add their lengths to `genericTypeLengths` / `channelTypeOverride`
3. Add `"EM310_TILT": ...milesight.Decode...` to `lnsParsers` in `registry/registry.go`
4. Add device entries with `"device_model": "em310_tilt"` in `devices.json`
5. In `record.go`: reuse an existing `SensorTypes` field where the physical
   quantity and unit already match (e.g. `battery_level`); add a new field
   only when nothing fits. Follow the [naming convention](#naming-convention-for-sensor_type) below.
   If the device reports more than one instance of the same physical
   quantity (e.g. multiple probes/valves), set `SensorDataRecord.DeviceIndex`
   per instance instead of baking an index into the `sensor_type` name — see
   the `DeviceIndex` rule in the naming convention section.

**Rules:**

- One file = one device codec. No internal submodel dispatch maps.
- Registry key = `MODEL_SUBMODEL` (uppercase). Fallback to `MODEL` handled by `lookupKeys`.
- Use underscore for transport differences on the same hardware (`ks3000_lora` / `ks3000_wifi`).
- Do not create placeholder files for unimplemented devices.
- Not every vendor uses a TLV wire format — `imt/imt.go` decodes a flat
  sequential-tag protocol instead of `[channel][type][data]` TLVs. The
  per-device decoder files still follow the same one-file-per-codec shape;
  only the shared parser underneath differs.
- Always emit the **raw** device value, untouched — never apply a curve-fit/
  scale correction in Go. That's a client-side/user-space concern for
  whatever consumes the SmartCampusMaua API, not something this pipeline
  computes or stores.
- If a channel's real-world meaning genuinely can't be known by the decoder
  (site-wired RS485/GPIO, e.g. UC100/UC300/UC501) rather than merely being
  multi-instance, use `ModbusChannel`/`IOChannel` + `device_channels.json`
  instead of `DeviceIndex` — see the [naming convention](#naming-convention-for-sensor_type)
  section for the distinction between the two mechanisms.

---

## Decode chain (end to end)

```
devices.json     assets.json    sensors.json         device_channels.json
  → GetDevicesMap() → GetAssetsMap() → GetSensorsMap()  → GetDeviceChannelsMap()
    deviceMap[..]=     deviceID ->      sensorMap[sensorID]  deviceChannels[deviceID]
    DeviceConfig       AssetConfig      = {SensorType,        = {Modbus[chn]->sensorID,
                                            DeviceID,             IO[chn]->sensorID}
                                            DeviceIndex,
                                            SensorName}
        ↓                   ↓                  ↓                        ↓
  mergeAssetInfo()    buildSensorIndex(sensorMap)
  backfills asset      → sensorIndex[deviceID+sensorType+deviceIndex] = sensorID
  info onto             (deviceIndex=0 for every single-instance sensor_type —
  DeviceConfig            behaves exactly like the old deviceID+sensorType key)

MQTT message arrives on  device/<identifier>/telemetry
  → detectProvider()           → "chirpstackv4" | "everynet" | "custom"
  → resolveIncomingDevice()    → devEUI lookup → deviceID + DeviceConfig
  → chirpstack.Parse() / everynet.Parse()  → LNSFrame{Data(b64), Port, Timestamp}
  → registry.DecodeLNS(model, submodel, payload, ...)
      → lookupKeys() = ["MODEL_SUBMODEL", "MODEL"]   (submodel key tried first)
      → lnsParsers["MODEL_SUBMODEL"]  → vendor.Decode()
          → vendor binaryDecoders["MODEL_SUBMODEL"]  → decodeXxx()
              → []SensorDataRecord   (UC100/300/501: ModbusChannel or IOChannel set, SensorType empty;
                                       everything else: SensorType set directly, DeviceIndex set when
                                       the device carries more than one sensor of that type)
  → per record, in parseDeviceModel:
      → ModbusChannel/IOChannel != 0?  resolveChannelRecord(): deviceChannels[deviceID].Modbus/.IO[chn]
                                        → sensorID → sensorMap[sensorID].SensorType
                                        → no config entry? record dropped (logged), never written
                                          under an empty/made-up sensor_type
      → otherwise:                     sensorIndex[deviceID+sensorType+deviceIndex] → SensorID (if registered)
  → writeSensorRecords()
      → device_model tag = lowercase(model + "_" + submodel)
      → sensor_data point per record (sensor_id tag set when resolved) → InfluxDB3 iot_sensors
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
| `_raw` suffix: value needs client-side calibration to be physically meaningful | `soil_moisture_raw` (raw ADC), vs. `soil_moisture` (already scaled on-device by `em500_smtc`) — see below |
| Legacy indexed components (pre-`DeviceIndex`): digit directly on the base, **no underscore before it** | `voltage_u1`, `power_p1`, `region1_occupancy` |

**The `_raw` suffix rule.** A `_raw`-suffixed name and its non-`_raw`
counterpart are **two different measurements**, not two names for the same
one — never merge them under a single `sensor_type` even when they describe
the same physical thing. `solenoid_valve_raw` (uncalibrated ADC, `lnv3_svc`
only) and `solenoid_valve_status` (the actual open/closed reading, reported
directly by `uc511` — its firmware does the ADC-to-status decision on-device,
so `uc511` has no raw signal to expose at all; `lnv3_svc` has no status
counterpart, since turning its raw reading into open/closed is a client-side
concern, not something this pipeline computes) is the clearest example: same
physical valve, genuinely different wire-level information.

**Indexing: `DeviceIndex` (new devices), not a baked-in digit (legacy).**
Every *new* device family added after this rule was adopted uses a single
generic `sensor_type` plus `SensorDataRecord.DeviceIndex` to tell multiple
same-type sensors on one device apart (e.g. `lnv3_sm3dl`'s three
`soil_moisture_raw` probes are `device_index` 1/2/3, not `smdl1/2/3`).
`DeviceIndex` is purely a **resolution key** — it disambiguates which
`sensor_id` a reading belongs to via `sensors.json`'s own `device_index`
field, and is **never written to InfluxDB3**; once `sensor_id` is resolved
it has done its only job. This is a different mechanism from
`ModbusChannel`/`IOChannel` (UC100/300/501): those exist because the
decoder *can't know* what a channel means at all (pure site config); `DeviceIndex`
exists for decoders that already know the physical quantity and position,
just need help telling same-type instances apart. The **legacy indexed
families** below (`voltage_u1/2/3` etc., from `ks3000_lora`/`ks3000_wifi`,
already shipped before this rule existed) keep their baked-in digits — they
were **not** retroactively migrated to `DeviceIndex`, since that would
rename `sensor_type` tag values already written to InfluxDB3 for live
devices; only genuinely new device families adopt the new pattern.
`power_factor1/2/3` and `digital_input1/2` were the only pre-existing
`sensor_type`s ever renamed outside a new-device addition — both because
neither had ever been emitted by any decoder yet (no live data to break):
first `power_factor_1`→`power_factor1` (dropped the underscore, joining the
sitewide legacy-indexing convention), then `digital_input1`/`digital_input2`
→ a single `digital_input` + `DeviceIndex` (joining the new pattern, once it
existed) when `uc511.go` needed the same concept.

`sensor_type` values are reused **across devices** when the physical
quantity and unit genuinely match — `battery_level` appears on more than one
device family below, and `air_temp` similarly covers `at101`'s ambient
reading alongside `nit21li_emw104`/`em300_di`'s.

`temperature_alarm` used to be reused this way too, but it wasn't a genuine
match: `em500_smtc`'s version is a **soil**-temperature alarm (emitted
alongside `soil_temp`, from its soil probe) while `at101`'s is an
**ambient**-air alarm — only the enum *shape* matched, not the physical
quantity. Split into `soil_temp_alarm` and `air_temp_alarm` respectively.
Like `power_factor1/2/3`/`digital_input1/2` above, this is a rename outside
a new-device addition — unlike those, though, live historical data already
exists under the old `temperature`/`temperature_alarm` tags for `at101` and
`em500_smtc`; only writes from the rename forward use the new names, so a
query spanning that boundary needs to account for both.

`electrical_conductivity` is currently
emitted by `em500_smtc` only, despite `record.go`'s field comment grouping
it with `em500_swl` — that grouping describes the shared "Water / soil"
struct section, not actual reuse; `em500_swl.go` never writes it.

`SensorTypes` fields that exist in `record.go` but are currently **not
emitted by any decoder** — grep `st.<Field>` across `go-parse/devices/` to
re-verify before relying on any of these:

- `water_flow`, `water_conv`, `pulse_conv` — reserved for EM300-DI v1.3+
  firmware's raw water-conversion channel, which is parsed but only used
  internally to compute `pulse_count`; the conversion factors themselves
  aren't written out today.
- `digital_input`, `interrupt_level`, `interrupt_status` — standardised
  DTL200-SWL I/O names, not emitted by `dtl200.go`; reserved for a probe
  variant that reads those pins. (`digital_input` is also emitted by
  `uc511.go` — the field is shared, `dtl200.go` just doesn't use it yet.)
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
| `soil_temp_alarm`      | —       | int (enum) | `0` release · `1` threshold · `2` mutation                  |

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

`digital_input`, `interrupt_level`, `interrupt_status` are defined as
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
| `air_temp`          | °C   | float      | Ambient temperature (plain channel or the temp+alarm channel); shared tag with `nit21li_emw104`/`em300_di` |
| `air_temp_alarm`    | —    | int (enum) | `0` normal · `1` abnormal                                          |
| `latitude`          | °    | float      | GPS/WiFi-resolved latitude, up to 6 decimal places (from normal `0x04` or alarm/geofence `0x84` report); not written when the device has no fix yet — see below |
| `longitude`         | °    | float      | GPS/WiFi-resolved longitude, same precision/fix caveat as `latitude`  |
| `motion_status`     | —    | int (enum) | `0` unknown · `1` start · `2` moving · `3` stop                     |
| `geofence_status`   | —    | int (enum) | `0` inside · `1` outside · `2` unset · `3` unknown                  |
| `device_position`   | —    | int (enum) | `0` normal · `1` tilt                                              |
| `tamper_status`     | —    | int (enum) | `0` install · `1` uninstall                                        |

`latitude`/`longitude` are `int32 / 1,000,000`, giving up to 6 decimal
places (~11 cm resolution) — full precision, not rounded. When the device
hasn't obtained a GPS/WiFi fix yet it sends raw `-1` (`0xFFFFFFFF`) for both
fields; the official Milesight decoder doesn't filter this and divides it
through anyway, producing a bogus `(-0.000001, -0.000001)` point that plots
at Null Island (0°N 0°E) on a geomap — easy to misread as "only showing one
decimal" since it displays as `~0.0` at typical rounding. `at101.go` detects
this sentinel and skips writing `latitude`/`longitude` for that uplink
(logging instead); `motion_status`/`geofence_status` are still written from
the same status byte, since they're independent of the GPS fix.

WiFi scan results (`0x06/0xD9`) are tokenized but not decoded — each result
carries a MAC address (not representable as float/int/bool) and a single
uplink can report several, which doesn't fit the one-value-per-sensor-type-
per-timestamp model this codebase uses everywhere else. History/backfill
records (`0x20/0xCE`, 12B here) have the same unresolved shared-length risk
described in the VS373 section above.

### `lnv3_sm3dl` — soil moisture, 3 depth levels (IMT LoraNodeV3-SM3DL)

Uses IMT's tag-based protocol (`imt/imt.go`), not Milesight TLV.

| sensor_type          | device_index | unit | value type | description                          |
| ----------------------- | -------------- | ------ | ------------ | ---------------------------------------- |
| `soil_moisture_raw`  | 1            | raw  | int        | Raw ADC reading, probe depth 1 (nominally 10 cm) |
| `soil_moisture_raw`  | 2            | raw  | int        | Raw ADC reading, probe depth 2 (nominally 30 cm) |
| `soil_moisture_raw`  | 3            | raw  | int        | Raw ADC reading, probe depth 3 (nominally 70 cm) |
| `board_voltage`      | —            | V    | float      | Board supply voltage                     |

`soil_moisture_raw` is intentionally **raw**, not a computed moisture
percentage — the conversion (`15000 × raw^power`, scaled per depth) is a
client-side/user-space concern applied by whatever consumes the
SmartCampusMaua API, not hardcoded in the decoder or computed anywhere in
this pipeline. (Formerly `smdl1/2/3` — renamed + indexed as part of the
`DeviceIndex` migration; see the
[naming convention](#naming-convention-for-sensor_type) section.)

### `lnv3_svc` — solenoid valve control (IMT LoraNodeV3-SVC)

| sensor_type              | device_index | unit | value type | description                          |
| --------------------------- | -------------- | ------ | ------------ | ---------------------------------------- |
| `solenoid_valve_raw`     | 1/2/3        | raw  | int        | Raw ADC reading, one per valve channel   |
| `pulse_count`            | —            | —    | int        | Solenoid actuation counter (reuses the same sensor_type as EM300-DI's pulse counter) |
| `board_voltage`          | —            | V    | float      | Board supply voltage                     |

`solenoid_valve_raw` is raw, like `lnv3_sm3dl`'s `soil_moisture_raw` (formerly
`sv1/2/3`) — `decodeLNV3SVC` only ever produces the raw ADC reading. The
open/closed decision is a client-side/user-space concern applied by whatever
consumes the SmartCampusMaua API; this pipeline doesn't compute or store it.
`uc511` is a different device with its own on-device open/closed status —
see the [naming convention](#naming-convention-for-sensor_type) section for
why `solenoid_valve_status` still exists as a `sensor_type`, just not for
`lnv3_svc`.

### `uc100` / `uc300` / `uc501` — RS485/Modbus controller (Milesight UC-series)

**No fixed `sensor_type` table** — unlike every other device above, what a
Modbus channel (or, on UC300/UC501, a GPIO/analog-input/PT100 pin) measures
is entirely site configuration, not something the decoder can know.
`decodeUC100`/`decodeUC300`/`decodeUC501` extract `(ModbusChannel, value)` or
`(IOChannel, value)` pairs only; `sensor_type` and `sensor_id` are filled in
afterward by `resolveChannelRecord` (main.go) from
[`device_channels.json`](#device-channel-registry-device_channelsjson)
+ [`sensors.json`](#sensor-registry-sensorsjson) — see the [decode chain](#decode-chain-end-to-end)
above. A channel with no `device_channels.json` entry is dropped (logged),
never written under an empty or made-up `sensor_type`. This is why none of
these three devices' fixed I/O needed a new `sensor_type` name discussed up
front, despite this repo's [naming convention](#naming-convention-for-sensor_type)
requiring exactly that for every other device: the physical meaning of a
channel is attributed entirely through `device_channels.json` + `sensors.json`
at deploy time (reusing an already-registered `sensor_type`, or a newly
discussed one), the same as a Modbus channel — never invented in Go. `uc501`
does have one fixed reading, `battery_level` — its own battery isn't
site-configurable the way an attached RS485/GPIO instrument is.

Wire format reference: `https://github.com/Milesight-IoT/SensorDecoders/tree/main/uc-series`.
UC100/UC300's Modbus channel entry is `[0xFF][0x19][channel_id][data_length
(unused on the wire — length is derived from data_type instead)][data_type:
bit7=sign, bits0-6=type][value: 1/2/4 bytes depending on type]`;
`[0xFF][0x15][channel_id]` reports a read error for that channel (logged, no
`sensor_data` point written). UC100 and UC300 firmware agree on the byte
length per `data_type` code but disagree on what two of those codes actually
mean — ported byte-for-byte per device, see the doc comments in
`uc100.go`/`uc300.go`.

**UC501's Modbus wire shape genuinely differs**, not just its value
semantics: a 2-byte header (`[modbus_chn_id][package_type]`, `data_type` in
`package_type`'s low 3 bits, no sign bit) instead of UC100/300's 3-byte
header, marked by channel_id `0xFF` **or** `0x80` (type `0x0E`) instead of
`0xFF`/`0x19` — and `0x80` carries one extra trailing alarm byte no other
variant has. `uc.go`'s shared tokenizer (`ParseUCTLV`) takes each model's
Modbus-marker check and entry-length function as parameters specifically to
accommodate this, rather than hardcoding UC100/300's shape — see the doc
comment at the top of `uc.go`.

UC300's fixed-hardware channels (`channel_id` 3-14: GPIO input/output,
PT100, ADC current/voltage) are decoded the same way, carrying `IOChannel`
instead of `ModbusChannel` — only their **plain instantaneous reading**
variant (`data_type` `0x00`/`0xC8`/`0x01`/`0x67`/`0x02`). The **statistics**
variant (`0xE2`: current value + max + min + avg, packed as four float16s)
is tokenized (so it doesn't corrupt the parse of whatever follows it) but
not decoded — a single `sensor_data` point can only carry one value per
`(sensor_type, timestamp)`, same reasoning as VS373's WiFi scan results.
UC100 has none of this hardware, so it's unaffected.

**UC501's fixed-hardware channels** (GPIO input/output/counter on
channel_id `0x03`/`0x04`; analog input on `0x05`/`0x06`, with an alarm-report
variant on `0x85`/`0x86` remapped to the same `IOChannel` as its normal
counterpart) are decoded the same way. Unlike UC300, UC501's analog input's
`0xE2` variant is a **primary firmware v3 reporting format**, not an optional
statistics overlay — so it *is* decoded (`f16le` in `uc.go`, Milesight's own
non-IEEE754 half-float encoding), taking only the current-value field and
ignoring the packed min/max/avg that ride along with it. UC501's SDI-12
probe channel (`0x08`/`0xDB`) is tokenized but not decoded — its payload is
an ASCII string, which doesn't fit this codebase's float/int/bool model.

### `uc511` / `uc512` — irrigation valve + pipe-pressure controller (Milesight UC511/UC512)

Fixed-purpose, unlike UC100/300/501 — **no RS485 port at all** (confirmed:
no Modbus marker anywhere in the official decoder), so `uc511.go` uses the
standard static-length `ParseMilesightTLV`, not `uc.go`. Every reading below
is device-intrinsic; nothing is site-configurable.

| sensor_type                    | device_index | unit | value type | description                          |
| ---------------------------------- | -------------- | ------ | ------------ | ---------------------------------------- |
| `battery_level`                 | —            | %    | float      | Battery charge percentage                |
| `solenoid_valve_status`         | 1/2          | —    | bool       | Open/closed, reported directly by the device — no raw counterpart exists |
| `pulse_count`                   | 1/2          | —    | int        | Per-valve actuation/flow pulse counter   |
| `digital_input`                 | 1/2          | —    | bool       | Auxiliary GPIO pin state (hw≥v2.0, fw≥v2.4) |
| `water_pressure`                | —            | —    | int        | Pipe pressure (unit per Milesight's own product doc — not stated in the decoder itself) |
| `pressure_sensor_fail_status`   | —            | —    | bool       | `true` = pressure sensor reporting an error |

A valve's raw wire byte `0xFF` means something entirely different from a
status reading — it's an echo of a downlink delay-control command's result
(success/failed), not a status — so `decodeUC511` skips it rather than
writing a fabricated `solenoid_valve_status`. Downlink command-response
echoes generally (LoRaWAN class-switch, schedule/multicast/AI-collection
config), `custom_message` (variable-length ASCII, no safe fixed length to
register), and history channels (`0x20/0xCE` — see the cross-device
collision note in `channelTypeOverride`; UC511 is a 5th device sharing that
same known gap) are not decoded into `sensor_data`.

### `vs370` — single-zone occupancy/illuminance presence sensor (Milesight VS370)

Despite the similar name, **not** a smaller VS373: VS373 is a multi-region
bed/room presence + vital-signs sensor with per-region booleans; VS370 has
exactly one occupancy reading and one illuminance reading, both simple
enums, no regions, no per-region indexing. Standard static-length
`ParseMilesightTLV`, no RS485.

| sensor_type          | unit | value type | description                                       |
| ----------------------- | ------ | ------------ | ------------------------------------------------------ |
| `battery_level`      | %    | float      | Battery charge percentage                          |
| `occupancy_status`   | —    | bool       | `true` = occupied, `false` = vacant                |
| `illuminance_status` | —    | int (enum) | `0` dim · `1` bright · `254` disable                |

`illuminance_status` is deliberately **not** the same `sensor_type` as
`nit21li_emw104`'s `illuminance` — that field is a continuous lux float;
this one is a discrete 3-value enum. Different value shape, can't share a
name (same `_raw`-suffix-style reasoning as above, just without the suffix
since neither variant is "raw").

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
      "battery_voltage_min": 3.3
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

## Sensor registry (`sensors.json`)

Canonical registry of every **sensor** (one measurement stream), decoupled
from `devices.json` on purpose. `sensor_id` is the **stable identity of a
measurement stream** — assigned once (UUIDv7, not derived/hashed) and never
recomputed; it persists across a device swap (repoint the `device_id` FK,
`sensor_id` itself doesn't change). `sensor_type` lives **only here** — not
duplicated into `device_channels.json`. This is a flat **pool** of sensors,
not a hierarchy — `device_id` is a foreign key marking which hardware
currently carries a sensor, not a container; a device is only "a hardware
platform to deploy sensors on the field."

Not unique on `(device_id, sensor_type)` alone — two sensors can legitimately
share both, disambiguated by `device_index`:

- **Same physical instance, multiple positions** (e.g. `lnv3_sm3dl`'s three
  `soil_moisture_raw` probes) — the *decoder itself* knows both the
  sensor_type and the position; `device_index` just picks the right
  `sensor_id`. This is the common case for non-UC devices.
- **Different physical instruments on one RS485 bus** (e.g. two identical
  Modbus sensors on one UC300) — the decoder knows *neither*; resolved
  instead by wire channel number via
  [Device channel registry](#device-channel-registry-device_channelsjson),
  which doesn't use `device_index` at all (the channel number already
  disambiguates).

Loaded at startup and reloaded on the same `DEVICE_REGISTRY_REFRESH_SEC`
interval as `devices.json`. Like `assets.json`, a bad or missing
`sensors.json` is **non-fatal** — logs a warning and runs with an empty
sensor index rather than crashing.

### Field reference

| field         | type   | required      | notes                                                        |
| --------------- | -------- | ---------------- | ----------------------------------------------------------------- |
| `version`     | string | —              | top-level; log-only; bump when schema changes                |
| `sensor_id`   | UUIDv7 | **mandatory**  | unique sensor identifier — stable identity of a measurement stream |
| `sensor_type` | string | **mandatory**  | must match a `record.SensorTypes` field (validated via `record.AllSensorTypes()`) |
| `device_id`   | UUIDv7 | **mandatory**  | FK → `devices.json`; soft/lenient — no error if the device isn't found |
| `device_index`| int    | optional       | disambiguates multiple sensors sharing `(device_id, sensor_type)`; omit for single-instance sensors — matches `record.SensorDataRecord.DeviceIndex`; never written to InfluxDB3 |
| `sensor_name` | string | optional       | purely cosmetic human label (e.g. `"Depth 10cm"`); never used for resolution, never written to InfluxDB3 |

### Full example

```json
{
  "version": "1",
  "sensors": [
    { "sensor_id": "01a03953-9387-76dd-bc9f-ee2b674fed66", "sensor_type": "water_level", "device_id": "01a03953-9387-7631-b960-5d7f9305a67c" },
    { "sensor_id": "01a03953-9387-76e9-b9dd-ce9662f2e172", "sensor_type": "battery_level", "device_id": "01a03953-9387-7631-b960-5d7f9305a67c" },
    { "sensor_id": "01a03992-e6c6-75d1-9ba4-0999e88e0c37", "sensor_type": "soil_moisture_raw", "device_id": "019f708d-1d7f-7d24-9029-e84713cddb96", "device_index": 1, "sensor_name": "Depth 10cm" }
  ]
}
```

**Validation rules (applied on every load):**

- `sensor_id` and `device_id` must be valid UUIDv7
- `sensor_type` must be a recognized `record.ST` value — add it to `record.go` first (after discussion; see [naming convention](#naming-convention-for-sensor_type)) if it's genuinely new
- `device_index` must not be negative
- Duplicate `sensor_id` causes the whole file to be rejected
- Duplicate `(device_id, sensor_type, device_index)` tuple causes the whole file to be rejected — a genuine config error, not legitimate multi-instance disambiguation
- A `device_id` with no matching `devices.json` entry is not an error — soft FK, same philosophy as `assets.json`

`buildSensorIndex` (main.go) derives a `(device_id, sensor_type, device_index)
-> sensor_id` lookup from this file for every non-UC decoder (`device_index`
defaults to `0` for single-instance sensors, matching the zero value decoders
leave on `SensorDataRecord.DeviceIndex` when they never set it — so this is
fully backward compatible with sensors that predate `device_index`). If a
tuple is *still* ambiguous even including `device_index` (shouldn't happen —
`GetSensorsMap` rejects duplicate tuples at load time — but this index
doesn't re-trust that), it's excluded (logged, not an error) rather than
resolved arbitrarily.

---

## Device channel registry (`device_channels.json`)

**UC100/UC300/UC501 only.** Routes a physical Modbus channel, or (UC300/UC501
only) a fixed-hardware GPIO/PT100/ADC/analog-input channel, to a `sensor_id`.
Deliberately carries **no** `sensor_type` — that lives only in
`sensors.json`, looked up via the `sensor_id` here. This is what lets the
resolution step tell two same-`sensor_type` instruments on one bus apart,
since `sensors.json` alone can't (see above).

None of these three decoders can know what any channel *means* — that's
entirely site config. Records from `decodeUC100`/`decodeUC300`/`decodeUC501`
carry `ModbusChannel` or `IOChannel` instead of `SensorType`;
`resolveChannelRecord` (main.go) looks up
`deviceChannels[deviceID].Modbus[channel]` or `.IO[channel]`
→ `sensor_id` → `sensors.json`'s `sensor_type`, filling both `SensorType` and
`SensorID` before write. **A channel with no entry here is dropped** (logged),
never written under an empty or made-up `sensor_type`.

Loaded at startup and reloaded on the same `DEVICE_REGISTRY_REFRESH_SEC`
interval. Non-fatal on a bad/missing file, same as `sensors.json`/`assets.json`.

### Field reference

| field             | type   | required                          | notes                                                  |
| ------------------- | -------- | ------------------------------------ | ----------------------------------------------------------- |
| `version`         | string | —                                  | top-level; log-only; bump when schema changes         |
| `device_id`       | UUIDv7 | **mandatory**                      | FK → `devices.json`, must be `uc100`, `uc300`, or `uc501` |
| `sensor_id`       | UUIDv7 | **mandatory**                      | FK → `sensors.json`                                    |
| `modbus_channel`  | int    | required for a Modbus entry (1-32) | must be set together with `modbus_slave_id`            |
| `modbus_slave_id` | int    | required for a Modbus entry        | documentation only — not read at decode time           |
| `io_channel`      | int    | required for an IO entry           | UC300/UC501 only — UC100 has no fixed I/O hardware. On UC300, covers GPIO input/output, PT100, and ADC current/voltage alike (channel_id 3-14, one namespace on the wire); on UC501, covers GPIO (channel_id 3-4) and analog input (channel_id 5-6, alarm-report variant on 0x85/0x86 remapped to the same channel) — see the `uc100`/`uc300`/`uc501` sensor type reference section above for each device's exact channel meanings |

Each entry is **either** a Modbus channel (`modbus_channel` + `modbus_slave_id`)
**or** an IO channel (`io_channel`) — never both, never neither.

### Full example

```json
{
  "version": "1",
  "channels": [
    { "device_id": "01a03953-9387-7631-b960-5d7f9305a67c", "sensor_id": "01a03953-9387-76dd-bc9f-ee2b674fed66", "modbus_channel": 3, "modbus_slave_id": 2 },
    { "device_id": "01a01fce-8746-7346-af7a-7db92b17cb72", "sensor_id": "01a03953-9387-76e9-b9dd-ce9662f2e172", "io_channel": 9 }
  ]
}
```

**Validation rules (applied on every load):**

- `device_id` and `sensor_id` must be valid UUIDv7
- Exactly one of (`modbus_channel` + `modbus_slave_id`) or `io_channel` must be set
- `io_channel` on a `device_id` known to be `device_model=uc100` is a **hard error** — UC100 has no fixed I/O hardware (this check only fires when the device is actually found; an unknown `device_id` is a soft FK like everywhere else)
- Duplicate `sensor_id`, or duplicate `modbus_channel`/`io_channel` on the same device, causes the whole file to be rejected

---

## Environment variables

| variable                      | default                        | description                                        |
| -------------------------------- | --------------------------------- | ------------------------------------------------------- |
| `MQTT_BROKER`                 | _(required)_                   | e.g. `tcp://broker:1883`                           |
| `INFLUXDB_HOST`               | `http://influxdb.maua.br:8181` | InfluxDB3 Core URL                                 |
| `INFLUXDB_TOKEN`              | _(empty)_                      | Auth token (may be empty for unauthenticated Core) |
| `DEVICE_REGISTRY_FILE`        | `devices.json`                 | Path to the JSON device registry                   |
| `ASSET_REGISTRY_FILE`         | `assets.json`                  | Path to the JSON asset registry                    |
| `SENSOR_REGISTRY_FILE`        | `sensors.json`                 | Path to the JSON sensor registry                    |
| `DEVICE_CHANNEL_REGISTRY_FILE`| `device_channels.json`         | Path to the JSON UC100/UC300/UC501 channel registry  |
| `DEVICE_REGISTRY_REFRESH_SEC` | `30`                            | Poll interval (seconds) for hot-reload of all four registries |
