// Package record defines shared types written to InfluxDB3 sensor_data measurement.
package record

import "time"

// ─── SensorDataRecord ────────────────────────────────────────────────────────

type SensorDataRecord struct {
	SensorType  string
	ValueType   string // "float" | "int" | "bool"
	ValueFloat  *float64
	ValueInt    *int64
	ValueBool   *bool
	DeviceModel string // lowercase merged model_submodel, e.g. "em300_di"
	DeviceID    string
	DevEUI      string // LoRaWAN devices only
	MacAddress  string // IP/WiFi devices only
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

// LNSFrame carries the decoded uplink data extracted from an LNS provider message.
// Both chirpstack and everynet parsers return this type.
type LNSFrame struct {
	Data      string    // base64-encoded LoRa payload
	Timestamp time.Time // frame timestamp from LNS metadata
	Port      uint64    // LoRaWAN fPort
}

func NewBool(sensorType, deviceModel, deviceID, provider string, value bool, ts time.Time) SensorDataRecord {
	v := value
	return SensorDataRecord{SensorType: sensorType, ValueType: "bool", ValueBool: &v,
		DeviceModel: deviceModel, DeviceID: deviceID, Provider: provider, Timestamp: ts}
}

// ─── SensorTypes ─────────────────────────────────────────────────────────────

// SensorTypes defines every canonical sensor_type tag value written to sensor_data.
// Use the ST singleton — never hardcode the string literals in parsers.
type SensorTypes struct {
	// ── IMT LoraNodeV3 / Soil Moisture 3 Depth Levels (LNV3-SM3DL) ─────────
	SMDL1        string //SoilMoisture @ 10 cm of Depth Level
	SMDL2        string // SoilMoisture @ 30 cm of Depth Level
	SMDL3        string // SoilMoisture @ 70 cm of Depth Level
	BoardVoltage string // board_voltage

	// ── IMT LoraNodeV3 / Solenoid Valve Control (LNV3-SVC) ───────────────
	SV1 string // Solenoid Valve 1
	SV2 string // Solenoid Valve 2
	SV3 string // Solenoid Valve 3
	// PulseCount string // pulse_count

	// ── Weather / environmental (Khomp NIT21LI-EMW104) ───────────────────
	InternalTemp string // internal_temp
	InternalRH   string // internal_rh
	AirTemp      string // air_temp
	AirRH        string // air_rh
	WindSpeed    string // wind_speed         m/s
	WindGust     string // wind_gust          m/s
	WindDir      string // wind_dir           °
	RainDepth    string // rain_depth         mm
	SolarRad     string // solar_rad          W/m²
	Illuminance  string // illuminance        lux
	UVIndex      string // uv_index
	AirPress     string // air_press          hPa

	// ── Device health ─────────────────────────────────────────────────────
	ExternalPower          string // external_power
	EnvSensorFailStatus    string // env_sensor_fail_status
	InternalBatteryVoltage string // internal_battery_voltage  V
	BatteryLevel           string // battery_level             %
	BatteryVoltage         string // battery_voltage           V
	C1State                string // c1_state
	C1Count                string // c1_count
	C2State                string // c2_state
	C2Count                string // c2_count

	// ── Three-phase power (KS3000, aggregate) ────────────────────────────
	VoltageULLAvg string // voltage_u_ll_avg   V  (U0)
	CurrentIAvg   string // current_i_avg      A  (I0)
	Frequency     string // frequency          Hz (F1)
	PowerPTotal   string // power_p_total      W  (P0)
	PowerQTotal   string // power_q_total      VAr(Q0)
	PowerFactor   string // power_factor           (FP0)
	EnergyAPlus   string // energy_a_plus      kWh
	EnergyQPlus   string // energy_q_plus      kVArh
	EnergyAMinus  string // energy_a_minus     kWh
	EnergyQMinus  string // energy_q_minus     kVArh
	ErrorCode     string // error_code

	// ── Per-phase voltages (KS3000) ───────────────────────────────────────
	VoltageU1  string // voltage_u1         V  (U1 L-N)
	VoltageU2  string // voltage_u2         V  (U2 L-N)
	VoltageU3  string // voltage_u3         V  (U3 L-N)
	VoltageU12 string // voltage_u12        V  (U12 L-L)
	VoltageU23 string // voltage_u23        V  (U23 L-L)
	VoltageU31 string // voltage_u31        V  (U31 L-L)

	// ── Per-phase currents (KS3000) ───────────────────────────────────────
	CurrentIN string // current_in         A  (neutral)
	CurrentI1 string // current_i1         A
	CurrentI2 string // current_i2         A
	CurrentI3 string // current_i3         A

	// ── Per-phase powers (KS3000) ─────────────────────────────────────────
	PowerP1      string // power_p1           W
	PowerP2      string // power_p2           W
	PowerP3      string // power_p3           W
	PowerQ1      string // power_q1           VAr
	PowerQ2      string // power_q2           VAr
	PowerQ3      string // power_q3           VAr
	PowerSTotal  string // power_s_total      VA  (S0)
	PowerS1      string // power_s1           VA
	PowerS2      string // power_s2           VA
	PowerS3      string // power_s3           VA
	PowerFactor1 string // power_factor1          (FP1)
	PowerFactor2 string // power_factor2
	PowerFactor3 string // power_factor3
	EnergySTotal string // energy_s_total     kVAh
	Horimetre    string // horimetre          h

	// ── Digital input / pulse (EM300-DI) ─────────────────────────────────
	PulseState string // pulse_state
	PulseCount string // pulse_count
	WaterFlow  string // water_flow         m³  (EM300-DI water mode)
	WaterConv  string // water_conv         m³/pulse conversion factor (v1.3+)
	PulseConv  string // pulse_conv         pulse conversion factor (v1.3+)

	// ── Water / soil (EM500-SWL, DTL200-SWL) ─────────────────────────────
	WaterLevel             string // water_level  m (EM500-SWL) | cm (DTL200 probe 0x00)
	ElectricalConductivity string // electrical_conductivity  µS/cm

	// ── Smart button (WS101) ──────────────────────────────────────────────
	PressType  string // press_type  1=single 2=long 3=double
	PressState string // press_state
	PressCount string // press_count

	// ── DTL200-SWL analog I/O ─────────────────────────────────────────────
	// ── DTL200-SWL standardised analog/digital I/O names ────────────────
	CurrentLoop     string // current_loop      mA   4-20 mA current-loop input
	VoltageInput    string // voltage_input      V   0-30 V analog voltage input
	DigitalInput1   string // digital_input1         IN1 pin state (bool)
	DigitalInput2   string // digital_input2         IN2 pin state (bool)
	InterruptLevel  string // interrupt_level        Exti pin level (bool)
	InterruptStatus string // interrupt_status       Exti trigger active (bool)
}

var ST = SensorTypes{
	SMDL1: "smdl1", SMDL2: "smdl2", SMDL3: "smdl3", BoardVoltage: "board_voltage",
	SV1: "sv1", SV2: "sv2", SV3: "sv3",

	InternalTemp: "internal_temp", InternalRH: "internal_rh",
	AirTemp: "air_temp", AirRH: "air_rh",
	WindSpeed: "wind_speed", WindGust: "wind_gust", WindDir: "wind_dir",
	RainDepth: "rain_depth", SolarRad: "solar_rad",
	Illuminance: "illuminance", UVIndex: "uv_index", AirPress: "air_press",
	ExternalPower: "external_power", EnvSensorFailStatus: "env_sensor_fail_status",
	InternalBatteryVoltage: "internal_battery_voltage",
	BatteryLevel:           "battery_level", BatteryVoltage: "battery_voltage",
	C1State: "c1_state", C1Count: "c1_count", C2State: "c2_state", C2Count: "c2_count",
	// aggregate power
	VoltageULLAvg: "voltage_u_ll_avg", CurrentIAvg: "current_i_avg",
	Frequency: "frequency", PowerPTotal: "power_p_total", PowerQTotal: "power_q_total",
	PowerFactor: "power_factor", EnergyAPlus: "energy_a_plus", EnergyQPlus: "energy_q_plus",
	EnergyAMinus: "energy_a_minus", EnergyQMinus: "energy_q_minus", ErrorCode: "error_code",
	// per-phase voltage
	VoltageU1: "voltage_u1", VoltageU2: "voltage_u2", VoltageU3: "voltage_u3",
	VoltageU12: "voltage_u12", VoltageU23: "voltage_u23", VoltageU31: "voltage_u31",
	// per-phase current
	CurrentIN: "current_in", CurrentI1: "current_i1", CurrentI2: "current_i2", CurrentI3: "current_i3",
	// per-phase power
	PowerP1: "power_p1", PowerP2: "power_p2", PowerP3: "power_p3",
	PowerQ1: "power_q1", PowerQ2: "power_q2", PowerQ3: "power_q3",
	PowerSTotal: "power_s_total", PowerS1: "power_s1", PowerS2: "power_s2", PowerS3: "power_s3",
	PowerFactor1: "power_factor1", PowerFactor2: "power_factor2", PowerFactor3: "power_factor3",
	EnergySTotal: "energy_s_total", Horimetre: "horimetre",
	// digital input
	PulseState: "pulse_state", PulseCount: "pulse_count", WaterFlow: "water_flow",
	WaterConv: "water_conv", PulseConv: "pulse_conv",
	// water / soil
	WaterLevel: "water_level", ElectricalConductivity: "electrical_conductivity",
	// button
	PressType: "press_type", PressState: "press_state", PressCount: "press_count",
	// DTL200 I/O
	// DTL200 I/O
	CurrentLoop: "current_loop", VoltageInput: "voltage_input",
	DigitalInput1: "digital_input1", DigitalInput2: "digital_input2",
	InterruptLevel: "interrupt_level", InterruptStatus: "interrupt_status",
}
