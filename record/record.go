// Package record defines shared types written to InfluxDB3 sensor_data measurement.
package record

import "time"

// ─── SensorDataRecord ────────────────────────────────────────────────────────

// SensorDataRecord is one row in the sensor_data InfluxDB3 measurement.
// Tags:   sensor_type, device_model, device_id, provider
// Fields: value_float | value_int | value_bool  (exactly one per record)
type SensorDataRecord struct {
	SensorType  string
	ValueType   string // "float" | "int" | "bool"
	ValueFloat  *float64
	ValueInt    *int64
	ValueBool   *bool
	DeviceModel string
	DeviceID    string
	Provider    string
	Timestamp   time.Time
}

func NewFloat(sensorType, deviceModel, deviceID, provider string, value float64, ts time.Time) SensorDataRecord {
	v := value
	return SensorDataRecord{SensorType: sensorType, ValueType: "float", ValueFloat: &v,
		DeviceModel: deviceModel, DeviceID: deviceID, Provider: provider, Timestamp: ts}
}

func NewInt(sensorType, deviceModel, deviceID, provider string, value int64, ts time.Time) SensorDataRecord {
	v := value
	return SensorDataRecord{SensorType: sensorType, ValueType: "int", ValueInt: &v,
		DeviceModel: deviceModel, DeviceID: deviceID, Provider: provider, Timestamp: ts}
}

func NewBool(sensorType, deviceModel, deviceID, provider string, value bool, ts time.Time) SensorDataRecord {
	v := value
	return SensorDataRecord{SensorType: sensorType, ValueType: "bool", ValueBool: &v,
		DeviceModel: deviceModel, DeviceID: deviceID, Provider: provider, Timestamp: ts}
}

// ─── AM19HexChannel ──────────────────────────────────────────────────────────

// AM19HexChannel is one decoded channel from an AM19-style hex-byte protocol
// (e.g. Milesight TLV).  Channel IDs are hex byte values; the type byte
// determines whether the channel carries a numeric Value or a boolean.
type AM19HexChannel struct {
	ID      byte
	IsBool  bool
	BoolVal bool
	Value   float64
}

// ─── SensorTypes ─────────────────────────────────────────────────────────────

// SensorTypes defines every canonical sensor_type tag value used in sensor_data.
// Field names follow the domain-parameter table in README.md.
// Use the package-level ST singleton; never hardcode the string values in parsers.
type SensorTypes struct {
	// ── Environmental / weather (README: Khomp NIT21LI + EMW104) ─────────
	InternalTemp           string // internal_temp  °C
	InternalRH             string // internal_rh    %
	AirTemp                string // air_temp        °C
	AirRH                  string // air_rh          %
	WindSpeed              string // wind_speed      m/s
	WindGust               string // wind_gust       m/s
	WindDir                string // wind_dir        °
	RainDepth              string // rain_depth      mm
	SolarRad               string // solar_rad       W/m²
	Illuminance            string // illuminance     lux
	UVIndex                string // uv_index        index
	AirPressure            string // air_pressure    hPa

	// ── Device health ─────────────────────────────────────────────────────
	ExternalPower          string // external_power           bool
	EnvSensorFailStatus    string // env_sensor_fail_status   bool
	InternalBatteryVoltage string // internal_battery_voltage V
	BatteryLevel           string // battery_level            %
	BatteryVoltage         string // battery_voltage          V
	C1State                string // c1_state bool
	C1Count                string // c1_count pulses
	C2State                string // c2_state bool
	C2Count                string // c2_count pulses

	// ── Power metering (README: Kron KS3000) ─────────────────────────────
	VoltageULLAvg          string // voltage_u_ll_avg  V
	CurrentIAvg            string // current_i_avg     A
	Frequency              string // frequency         Hz
	PowerPTotal            string // power_p_total     kW
	PowerQTotal            string // power_q_total     kvar
	PowerFactor            string // power_factor      —
	EnergyAPlus            string // energy_a_plus     kWh
	EnergyQPlus            string // energy_q_plus     kvarh
	EnergyAMinus           string // energy_a_minus    kWh
	EnergyQMinus           string // energy_q_minus    kvarh
	ErrorCode              string // error_code        —

	// ── Digital input / pulse counting (Milesight EM300-DI) ──────────────
	PulseState             string // pulse_state    bool
	PulseCounter           string // pulse_counter  pulses

	// ── Water / soil level (Milesight EM500-SWL, Dragino DTL200-SWL) ─────
	WaterLevel             string // water_level   cm | % VWC | MPa | Pa
	ElectricalConductivity string // electrical_conductivity  µS/cm

	// ── Smart button (Milesight WS101-R) ─────────────────────────────────
	PressType              string // press_type  int (1=single,2=long,3=double)
	PressState             string // press_state bool
	PressCount             string // press_count cumulative

	// ── DTL200-SWL analog I/O ─────────────────────────────────────────────
	IdcInputMA             string // idc_input_ma  mA
	VdcInputV              string // vdc_input_v   V
	IN1PinHigh             string // in1_pin_high  bool
	IN2PinHigh             string // in2_pin_high  bool
	ExtiStatus             string // exti_status   bool
}

// ST is the package-level SensorTypes singleton.
// All parser packages reference this instead of hardcoding string literals.
var ST = SensorTypes{
	InternalTemp:           "internal_temp",
	InternalRH:             "internal_rh",
	AirTemp:                "air_temp",
	AirRH:                  "air_rh",
	WindSpeed:              "wind_speed",
	WindGust:               "wind_gust",
	WindDir:                "wind_dir",
	RainDepth:              "rain_depth",
	SolarRad:               "solar_rad",
	Illuminance:            "illuminance",
	UVIndex:                "uv_index",
	AirPressure:            "air_pressure",
	ExternalPower:          "external_power",
	EnvSensorFailStatus:    "env_sensor_fail_status",
	InternalBatteryVoltage: "internal_battery_voltage",
	BatteryLevel:           "battery_level",
	BatteryVoltage:         "battery_voltage",
	C1State:                "c1_state",
	C1Count:                "c1_count",
	C2State:                "c2_state",
	C2Count:                "c2_count",
	VoltageULLAvg:          "voltage_u_ll_avg",
	CurrentIAvg:            "current_i_avg",
	Frequency:              "frequency",
	PowerPTotal:            "power_p_total",
	PowerQTotal:            "power_q_total",
	PowerFactor:            "power_factor",
	EnergyAPlus:            "energy_a_plus",
	EnergyQPlus:            "energy_q_plus",
	EnergyAMinus:           "energy_a_minus",
	EnergyQMinus:           "energy_q_minus",
	ErrorCode:              "error_code",
	PulseState:             "pulse_state",
	PulseCounter:           "pulse_counter",
	WaterLevel:             "water_level",
	ElectricalConductivity: "electrical_conductivity",
	PressType:              "press_type",
	PressState:             "press_state",
	PressCount:             "press_count",
	IdcInputMA:             "idc_input_ma",
	VdcInputV:              "vdc_input_v",
	IN1PinHigh:             "in1_pin_high",
	IN2PinHigh:             "in2_pin_high",
	ExtiStatus:             "exti_status",
}
