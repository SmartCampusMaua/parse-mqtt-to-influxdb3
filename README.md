# parse-mqtt-to-influxdb3

Subscribes to MQTT, auto-detects the message provider, decodes the binary or
JSON payload, and writes one **`sensor_data`** point per decoded field directly
to an InfluxDB3 Core instance.

---

## InfluxDB3 measurement schema

| element | value |
|---|---|
| measurement | `sensor_data` |
| tags | `sensor_type`, `device_model`, `device_id`, `provider` |
| fields | `value_float` **or** `value_int` **or** `value_bool` (one per row) |
| timestamp | nanosecond precision, sourced from the LNS frame or device message |

---

## Naming convention

`sensor_type` names follow the pattern **`<domain>_<parameter>`** where
*parameter* uses the standard domain abbreviation when one is established:

| rule | example |
|---|---|
| Use standard abbreviation | `air_temp` (not `air_temperature`) |
| Use WMO/ISO symbol | `air_rh` (RH = relative humidity symbol) |
| Drop suffix when root is unambiguous | `air_press` (`press·ure`), `solar_rad` (`rad·iation`) |
| Keep full word when no shorter standard exists | `illuminance`, `wind_speed`, `water_level` |

> **`air_press` vs `air_pressure`** — `air_press` was chosen for consistency
> with the abbreviation pattern already set by `air_temp`, `air_rh`, and
> `solar_rad`.  The root `press` is unambiguous in any measurement context and
> matches instrumentation convention (`press. sensor`, `press. transducer`).

---

## Sensor type reference

All values below are tag values for `sensor_type` in the `sensor_data`
measurement.  They are defined as named fields in `record.SensorTypes` (see
`record/record.go`) — never as bare string literals — so any typo is caught at
compile time.

### Weather / environmental  — `nit21li_emw104`

| sensor_type | unit | value type | description |
|---|---|---|---|
| `internal_temp` | °C | float | Internal device / enclosure temperature |
| `internal_rh` | % | float | Internal relative humidity |
| `air_temp` | °C | float | Ambient air temperature |
| `air_rh` | % | float | Ambient relative humidity in air |
| `wind_speed` | m/s | float | Average wind speed over interval |
| `wind_gust` | m/s | float | Peak wind speed (gust) during interval |
| `wind_dir` | ° | float | Wind direction (0° = North, clockwise) |
| `rain_depth` | mm | float | Accumulated rainfall depth |
| `solar_rad` | W/m² | float | Global solar radiation (shortwave) |
| `illuminance` | lux | float | Ambient light level |
| `uv_index` | — | float | Ultraviolet radiation exposure index |
| `air_press` | hPa | float | Atmospheric (barometric) pressure |

### Device health — `nit21li_emw104`

| sensor_type | unit | value type | description |
|---|---|---|---|
| `external_power` | — | bool | `true` = external power; `false` = battery |
| `env_sensor_fail_status` | — | bool | `true` = sensor failure detected |
| `internal_battery_voltage` | V | float | Voltage of internal backup battery |
| `c1_state` | — | bool | Digital input 1 state (`false` = open, `true` = closed) |
| `c1_count` | pulses | int | Cumulative pulse count from digital input 1 |
| `c2_state` | — | bool | Digital input 2 state |
| `c2_count` | pulses | int | Cumulative pulse count from digital input 2 |

### Power metering — `ks3000_lora`, `ks3000_wifi`

Both KS3000 variants (LoRaWAN binary and direct MQTT) produce identical
`sensor_type` values, enabling unified queries with
`WHERE device_model LIKE 'ks3000%'`.

| sensor_type | unit | value type | description |
|---|---|---|---|
| `voltage_u_ll_avg` | V | float | Average line-to-line voltage |
| `current_i_avg` | A | float | Average current across phases |
| `frequency` | Hz | float | Power system frequency |
| `power_p_total` | kW | float | Total active power |
| `power_q_total` | kvar | float | Total reactive power |
| `power_factor` | — | float | Ratio of active to apparent power |
| `energy_a_plus` | kWh | float | Active energy import |
| `energy_q_plus` | kvarh | float | Reactive energy import |
| `energy_a_minus` | kWh | float | Active energy export |
| `energy_q_minus` | kvarh | float | Reactive energy export |
| `error_code` | — | float | Device or power-quality error code (0 = no error) |

### Digital input / pulse counting — `em300_di`

| sensor_type | unit | value type | description |
|---|---|---|---|
| `battery_level` | % | float | Battery charge percentage |
| `air_temp` | °C | float | Ambient temperature (if temp probe present) |
| `air_rh` | % | float | Ambient relative humidity (if humi probe present) |
| `pulse_state` | — | bool | GPIO digital input state |
| `pulse_counter` | pulses | int | Cumulative pulse count |

### Water / soil level — `em500_swl`

| sensor_type | unit | value type | description |
|---|---|---|---|
| `battery_level` | % | float | Battery charge percentage |
| `air_temp` | °C | float | Soil probe temperature |
| `water_level` | % VWC | float | Volumetric water content (soil moisture) |
| `electrical_conductivity` | µS/cm | float | Soil electrical conductivity |

### Water / liquid level — `dtl200_swl`

Probe mode determines the unit of `water_level`:
`0x00` → cm depth · `0x01` → MPa pressure · `0x02` → Pa differential pressure

| sensor_type | unit | value type | description |
|---|---|---|---|
| `battery_voltage` | V | float | Battery voltage |
| `idc_input_ma` | mA | float | 4–20 mA current loop input |
| `vdc_input_v` | V | float | Voltage input |
| `water_level` | cm / MPa / Pa | float | Calculated water level (probe-mode dependent) |
| `in1_pin_high` | — | bool | IN1 pin state |
| `in2_pin_high` | — | bool | IN2 pin state |
| `exti_status` | — | bool | External trigger / interrupt status |

### Smart button — `ws101_r`

| sensor_type | unit | value type | description |
|---|---|---|---|
| `battery_level` | % | float | Battery charge percentage |
| `press_type` | — | int | Press type: `1` single · `2` long · `3` double |
| `press_state` | — | bool | Immediate press event |
| `press_count` | — | int | Cumulative press count |

---

## Provider detection

`sensor_type` is detected from the MQTT message body — never from the topic
path — so devices can migrate between LoRa networks without reconfiguration.

| provider tag | format | fingerprint |
|---|---|---|
| `chirpstackv4` | JSON object | root-level `deduplicationId` UUID |
| `everynet` | JSON object | `meta.device` + `params.payload` present |
| `custom` | JSON array | first element has `variable: "data"` |

---

## Environment variables

| variable | default | description |
|---|---|---|
| `MQTT_BROKER` | *(required)* | e.g. `tcp://broker:1883` |
| `INFLUXDB_HOST` | `http://influxdb.maua.br:8181` | InfluxDB3 Core URL |
| `INFLUXDB_TOKEN` | *(empty)* | Auth token (may be empty for unauthenticated Core) |
| `INFLUXDB_DATABASE` | `iot_rp40d` | Database / bucket name |
| `INFLUXDB_ORG` | `IMT` | Organisation name |

---

## Project layout

```
.
├── main.go                   MQTT loop · provider detection · InfluxDB3 writer
│                             DeviceModel registry (GetDevicesMap)
├── record/record.go          SensorDataRecord · SensorTypes (ST)
└── go-parse/
    ├── khomp/khomp.go        KhompSensors · DecodeNIT21LI_EMW104
    ├── kron/kron.go          DecodeLoRa · DecodeWiFi (KS3000)
    ├── milesight/milesight.go DecodeEM300DI · DecodeEM500SWL · DecodeWS101R
    └── dragino/dragino.go    DecodeDTL200SWL
```

---

## KS3000-LoRa additional sensor types

The KS3000-LoRa protocol sends per-phase and aggregate measurements encoded as
a 3-byte custom float (top 3 bytes of IEEE 754 float32 big-endian).

### KS3000-LoRa — per-phase voltages

| sensor_type | unit | description |
|---|---|---|
| `voltage_u_ll_avg` | V | Three-phase average (U0) |
| `voltage_u1` | V | Phase 1 voltage L-N (U1) |
| `voltage_u2` | V | Phase 2 voltage L-N |
| `voltage_u3` | V | Phase 3 voltage L-N |
| `voltage_u12` | V | Line L1-L2 (U12) |
| `voltage_u23` | V | Line L2-L3 |
| `voltage_u31` | V | Line L3-L1 |

### KS3000-LoRa — currents

| sensor_type | unit | description |
|---|---|---|
| `current_i_avg` | A | Three-phase (I0) |
| `current_in` | A | Neutral (IN) |
| `current_i1` | A | Phase 1 (I1) |
| `current_i2` | A | Phase 2 |
| `current_i3` | A | Phase 3 |

### KS3000-LoRa — power & energy

| sensor_type | unit | description |
|---|---|---|
| `power_p_total` | W | Total active (P0) |
| `power_p1/p2/p3` | W | Per-phase active |
| `power_q_total` | VAr | Total reactive (Q0) |
| `power_q1/q2/q3` | VAr | Per-phase reactive |
| `power_s_total` | VA | Total apparent (S0) |
| `power_s1/s2/s3` | VA | Per-phase apparent |
| `power_factor` | — | Three-phase PF (FP0) |
| `power_factor_1/2/3` | — | Per-phase PF |
| `energy_a_plus` | kWh | Import active (EA) |
| `energy_q_plus` | kVArh | Import reactive (ER) |
| `energy_a_minus` | kWh | Export active (EAN) |
| `energy_q_minus` | kVArh | Export reactive (ERN) |
| `energy_s_total` | kVAh | Apparent energy (ES) |
| `error_code` | — | Device error code (0=ok) |
| `horimetre` | h | Hour meter |
| `air_temp` | °C | Internal temperature |

### DTL200-SWL — water_level units per probe mode

| probe_mode (bytes[3]) | `water_level` unit |
|---|---|
| `0x00` depth | **cm**   (= (IDC−4) × (range\_key×100/16)) |
| `0x01` pressure | **MPa** |
| `0x02` differential | **Pa** |

> **Zero water_level?** Check `idc_input_ma` in sensor_data:
> - `≤ 4.0 mA` → sensor at/below zero point (correct, no water)
> - `> 4.0 mA` AND zero level → `bytes[3]` (range key) = 0, probe range not configured in device

### EM300-DI — water-flow mode (v1.3+)

When the EM300-DI is in flow-meter mode (channel `0x05 type 0xE1`):

| sensor_type | unit | description |
|---|---|---|
| `water_flow` | m³ | Converted water volume (float32 LE) |
