package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	influxdb3 "github.com/InfluxCommunity/influxdb3-go/v2/influxdb3"
	MQTT "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/devices/registry"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/providers/chirpstack"
	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/providers/everynet"
	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

// ─────────────────────────────────────────────────────────────────────────────
// Multi-database InfluxDB3 client pool
// ─────────────────────────────────────────────────────────────────────────────

// DBClients holds one client per logical database category.
// IoT sensor data and audit logs are separated so each can carry its own
// retention policy (short for audit, longer for sensor_data).
// The two clients may point to the SAME InfluxDB3 instance (same host, different
// database names) or to DIFFERENT instances (different ports/hosts) — controlled
// entirely by environment variables.
// DBClients holds four InfluxDB3 clients for different data streams.
// All clients connect to the same InfluxDB3 host with the same token,
// but write to different hardcoded databases.
type DBClients struct {
	IoTSensors       *influxdb3.Client // iot_sensors — sensor_data + sensor_calibration
	AuditIoT         *influxdb3.Client // audit_iot — raw MQTT payload ingest
	VehicleTelemetry *influxdb3.Client // vehicle_telemetry — vehicle GPS/CAN data
	AuditVehicle     *influxdb3.Client // audit_vehicle — raw vehicle audit logs
}

func (c *DBClients) Close() {
	_ = c.IoTSensors.Close()
	_ = c.AuditIoT.Close()
	_ = c.VehicleTelemetry.Close()
	_ = c.AuditVehicle.Close()
}

// Package main — device registry.
//
// Single source of truth for:
//   – hardware model constants (DeviceModel)
//   – per-sensor calibration parameters: scale, offset, power
//     formula (applied by users when plotting):
//       calibrated = (raw ^ power) × scale + offset
//     defaults (scale=1, offset=0, power=1) leave raw values unchanged.
//   – battery voltage range for battery_level % calculation
//
// ┌── How to add a new device ────────────────────────────────────────────────┐
// │  1. Add an ID entry: "device_eui": d(MODEL)                              │
// │  2. To calibrate a sensor: d(MODEL, cal("sensor_type", scale, off, pow)) │
// │  Example – water meter (EM300-DI): 1 pulse = 1 litre = 0.001 m³         │
// │    "24e124136f315508": d(EM300, cal("pulse_counter", 0.001, 0, 1))      │
// └───────────────────────────────────────────────────────────────────────────┘

// ─── Device model constants ───────────────────────────────────────────────────

// DeviceModel is a typed string identifying the hardware model.
// It is the sole routing key in parseDeviceModel.
type DeviceModel string

const (
	LNV3    DeviceModel = "LNV3"    // IMT LoraNodeV3 series
	DTL200  DeviceModel = "DTL200"  // Khomp DTL200 series
	EM300   DeviceModel = "EM300"   // Milesight EM300 series
	EM500   DeviceModel = "EM500"   // Milesight EM500 series
	KS3000  DeviceModel = "KS3000"  // Kron KS3000 series
	NIT21LI DeviceModel = "NIT21LI" // Khomp NIT21LI model
	WS101   DeviceModel = "WS101"   // Milesight smart button model
	UC100   DeviceModel = "UC100"   // Milesight UC100 RS485/Modbus controller (Modbus-only, no GPIO)
	UC300   DeviceModel = "UC300"   // Milesight UC300 RS485/Modbus controller (Modbus + fixed GPIO/PT100/ADC)
)

// ─── Calibration types ────────────────────────────────────────────────────────

// DeviceCalibration holds the sensor_calibration parameters for one sensor.
// Entries are written to the sensor_calibration measurement at startup.
// Scale, Offset, Power default to 1, 0, 1 (identity: output = input).
type DeviceCalibration struct {
	SensorType string  `json:"sensor_type"`
	Scale      float64 `json:"scale"`  // multiply factor  — default 1.0
	Offset     float64 `json:"offset"` // additive offset   — default 0.0
	Power      float64 `json:"power"`  // exponent          — default 1.0
}

// DeviceConfig bundles model, battery range, and optional calibrations.
type DeviceConfig struct {
	Model             DeviceModel
	DeviceType        string // "LORAWAN" or "IP"
	SerialNumber      string
	MacAddress        string // IP/WiFi devices — written as mac_address InfluxDB3 tag
	AssetID           string
	AssetCoords       AssetCoords
	BatteryVoltageMax float64 // V, full charge  (default 4.2 — LiPo/Li-Ion)
	BatteryVoltageMin float64 // V, cutoff       (default 3.3 — LoRa safe minimum)
	ProbeRangeM       float64 // DTL200: overrides bytes[3] when 0 (not configured via downlink)
	Calibrations      []DeviceCalibration
}

// ─── Device registry ─────────────────────────────────────────────────────────

// DeviceEntry describes one device loaded from the external registry file.
// If Allow is nil or true, the device is active; false disables ingestion.
// Asset assignment (asset_id, asset_coords) lives in the separate asset
// registry (assets.json), not here — see AssetEntry.
type DeviceEntry struct {
	Allow             *bool               `json:"allow,omitempty"`
	DeviceID          string              `json:"device_id,omitempty"`
	DeviceType        string              `json:"device_type,omitempty"` // "lorawan" or "ip"
	SerialNumber      *string             `json:"serial_number"`         // required; warn if empty
	DevEUI            string              `json:"dev_eui,omitempty"`     // mandatory for lorawan
	MacAddress        string              `json:"mac_address,omitempty"` // mandatory for ip
	DeviceModel       DeviceModel         `json:"device_model,omitempty"`
	BatteryVoltageMax float64             `json:"battery_voltage_max,omitempty"`
	BatteryVoltageMin float64             `json:"battery_voltage_min,omitempty"`
	ProbeRangeM       float64             `json:"probe_range_m,omitempty"`
	Calibrations      []DeviceCalibration `json:"calibrations,omitempty"`
}

// AssetCoords stores optional asset geolocation metadata.
type AssetCoords struct {
	Lng float64 `json:"lng,omitempty"`
	Lat float64 `json:"lat,omitempty"`
	Alt float64 `json:"alt,omitempty"`
}

type DeviceRegistryFile struct {
	Version string        `json:"version,omitempty"`
	Devices []DeviceEntry `json:"devices"`
}

// ─── Asset registry ───────────────────────────────────────────────────────────

// AssetEntry describes one physical asset loaded from the external asset
// registry file. DeviceIDs lists every device_id (from devices.json) mounted
// on this asset — the cross-reference lives here, not on the device.
type AssetEntry struct {
	AssetID     string       `json:"asset_id,omitempty"`
	AssetCoords *AssetCoords `json:"asset_coords,omitempty"`
	DeviceIDs   []string     `json:"device_ids,omitempty"`
}

type AssetRegistryFile struct {
	Version string       `json:"version,omitempty"`
	Assets  []AssetEntry `json:"assets"`
}

// AssetConfig is the per-device projection of AssetEntry resolved by
// GetAssetsMap: which asset a device is mounted on, and where that asset is.
type AssetConfig struct {
	AssetID     string
	AssetCoords AssetCoords
}

// ─── Registry validation ──────────────────────────────────────────────────────

var reDevEUI = regexp.MustCompile(`(?i)^[0-9a-f]{16}$`)

func isUUIDv7(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id.Version() == 7
}

// validateEntry checks mandatory fields and format constraints for one device.
// Returns an error for hard failures; logs warnings for soft issues.
func validateEntry(e DeviceEntry) error {
	if e.DeviceID == "" {
		return fmt.Errorf("device_id is required")
	}
	if !isUUIDv7(e.DeviceID) {
		return fmt.Errorf("device %s: device_id must be a valid UUIDv7", e.DeviceID)
	}
	if e.DeviceModel == "" {
		return fmt.Errorf("device %s: device_model is required", e.DeviceID)
	}

	dt := strings.ToUpper(e.DeviceType)
	if dt != "LORAWAN" && dt != "IP" {
		return fmt.Errorf("device %s: device_type must be \"lorawan\" or \"ip\", got %q", e.DeviceID, e.DeviceType)
	}

	if dt == "LORAWAN" {
		if e.DevEUI == "" {
			return fmt.Errorf("device %s: dev_eui is required for lorawan devices", e.DeviceID)
		}
		if !reDevEUI.MatchString(e.DevEUI) {
			return fmt.Errorf("device %s: dev_eui %q must be 16 hex characters (EUI-64)", e.DeviceID, e.DevEUI)
		}
	}

	if dt == "IP" {
		if e.MacAddress == "" {
			return fmt.Errorf("device %s: mac_address is required for ip devices", e.DeviceID)
		}
		if _, err := net.ParseMAC(e.MacAddress); err != nil {
			return fmt.Errorf("device %s: mac_address %q is invalid: %w", e.DeviceID, e.MacAddress, err)
		}
	}

	if e.SerialNumber == nil {
		return fmt.Errorf("device %s: serial_number field is required (use empty string if unknown)", e.DeviceID)
	}
	if *e.SerialNumber == "" {
		log.Printf("[registry] warning: device %s (%s) has no serial_number", e.DeviceID, e.DeviceModel)
	}

	return nil
}

// GetDevicesMap loads the canonical device registry from a JSON file.
// Returns two maps:
// - deviceID -> DeviceConfig
// - devEUI   -> deviceID
// All mandatory fields are validated; the whole file is rejected on any error.
func GetDevicesMap(registryPath string) (map[string]DeviceConfig, map[string]string, error) {
	contents, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read registry file: %w", err)
	}

	var reg DeviceRegistryFile
	if err := json.Unmarshal(contents, &reg); err != nil {
		return nil, nil, fmt.Errorf("parse registry file: %w", err)
	}

	if reg.Version == "" {
		log.Printf("[registry] warning: devices.json has no version field")
	} else {
		log.Printf("[registry] version=%s devices=%d", reg.Version, len(reg.Devices))
	}

	deviceMap := make(map[string]DeviceConfig, len(reg.Devices))
	devEUIToDeviceID := make(map[string]string, len(reg.Devices))

	for i, entry := range reg.Devices {
		if entry.Allow != nil && !*entry.Allow {
			continue
		}

		if err := validateEntry(entry); err != nil {
			return nil, nil, fmt.Errorf("entry[%d]: %w", i, err)
		}

		deviceID := entry.DeviceID
		deviceModel := entry.DeviceModel

		if _, exists := deviceMap[deviceID]; exists {
			return nil, nil, fmt.Errorf("duplicate device_id in registry: %s", deviceID)
		}

		batteryMax := entry.BatteryVoltageMax
		if batteryMax == 0 {
			batteryMax = 4.2
		}
		batteryMin := entry.BatteryVoltageMin
		if batteryMin == 0 {
			batteryMin = 3.3
		}

		serialNumber := ""
		if entry.SerialNumber != nil {
			serialNumber = *entry.SerialNumber
		}

		calibrations := make([]DeviceCalibration, 0, len(entry.Calibrations))
		for _, c := range entry.Calibrations {
			if c.SensorType == "" {
				continue
			}
			// Identity calibration does not need to be written/stored.
			if c.Scale == 1.0 && c.Offset == 0.0 && c.Power == 1.0 {
				continue
			}
			calibrations = append(calibrations, c)
		}

		deviceMap[deviceID] = DeviceConfig{
			Model:             deviceModel,
			DeviceType:        strings.ToUpper(entry.DeviceType),
			SerialNumber:      serialNumber,
			MacAddress:        strings.ToLower(entry.MacAddress),
			BatteryVoltageMax: batteryMax,
			BatteryVoltageMin: batteryMin,
			ProbeRangeM:       entry.ProbeRangeM,
			Calibrations:      calibrations,
		}

		if entry.DevEUI != "" {
			devEUINormalized := strings.ToLower(entry.DevEUI)
			if mapped, exists := devEUIToDeviceID[devEUINormalized]; exists && mapped != deviceID {
				return nil, nil, fmt.Errorf("duplicate dev_eui mapping for %s (%s and %s)", devEUINormalized, mapped, deviceID)
			}
			devEUIToDeviceID[devEUINormalized] = deviceID
		}
	}

	return deviceMap, devEUIToDeviceID, nil
}

// validateAssetEntry checks mandatory fields and format constraints for one
// asset. Returns an error for hard failures; the caller decides how to log.
func validateAssetEntry(a AssetEntry) error {
	if a.AssetID == "" {
		return fmt.Errorf("asset_id is required")
	}
	if !isUUIDv7(a.AssetID) {
		return fmt.Errorf("asset %s: asset_id must be a valid UUIDv7", a.AssetID)
	}
	if a.AssetCoords != nil {
		if a.AssetCoords.Lat < -90 || a.AssetCoords.Lat > 90 {
			return fmt.Errorf("asset %s: asset_coords.lat %f out of range [-90, 90]", a.AssetID, a.AssetCoords.Lat)
		}
		if a.AssetCoords.Lng < -180 || a.AssetCoords.Lng > 180 {
			return fmt.Errorf("asset %s: asset_coords.lng %f out of range [-180, 180]", a.AssetID, a.AssetCoords.Lng)
		}
	}
	for _, deviceID := range a.DeviceIDs {
		if !isUUIDv7(deviceID) {
			return fmt.Errorf("asset %s: device_ids contains invalid UUIDv7 %q", a.AssetID, deviceID)
		}
	}
	return nil
}

// GetAssetsMap loads the physical asset registry from a JSON file and
// resolves it into a deviceID -> AssetConfig lookup via each asset's
// device_ids cross-reference. All mandatory fields are validated; the whole
// file is rejected on any error.
func GetAssetsMap(registryPath string) (map[string]AssetConfig, error) {
	contents, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, fmt.Errorf("read asset registry file: %w", err)
	}

	var reg AssetRegistryFile
	if err := json.Unmarshal(contents, &reg); err != nil {
		return nil, fmt.Errorf("parse asset registry file: %w", err)
	}

	if reg.Version == "" {
		log.Printf("[registry] warning: assets.json has no version field")
	} else {
		log.Printf("[registry] version=%s assets=%d", reg.Version, len(reg.Assets))
	}

	seenAssetID := make(map[string]bool, len(reg.Assets))
	deviceToAsset := make(map[string]AssetConfig)

	for i, a := range reg.Assets {
		if err := validateAssetEntry(a); err != nil {
			return nil, fmt.Errorf("entry[%d]: %w", i, err)
		}
		if seenAssetID[a.AssetID] {
			return nil, fmt.Errorf("duplicate asset_id in registry: %s", a.AssetID)
		}
		seenAssetID[a.AssetID] = true

		coords := AssetCoords{}
		if a.AssetCoords != nil {
			coords = *a.AssetCoords
		}
		cfg := AssetConfig{AssetID: a.AssetID, AssetCoords: coords}

		for _, deviceID := range a.DeviceIDs {
			if existing, exists := deviceToAsset[deviceID]; exists {
				log.Printf("[registry] warning: device %s listed under multiple assets (%s and %s) — using %s",
					deviceID, existing.AssetID, a.AssetID, a.AssetID)
			}
			deviceToAsset[deviceID] = cfg
		}
	}

	return deviceToAsset, nil
}

// mergeAssetInfo backfills AssetID/AssetCoords onto each device from the
// asset registry's device_ids cross-reference. Devices not listed under any
// asset keep the zero value — asset assignment is optional.
func mergeAssetInfo(deviceMap map[string]DeviceConfig, assetByDeviceID map[string]AssetConfig) {
	for deviceID, cfg := range deviceMap {
		if a, ok := assetByDeviceID[deviceID]; ok {
			cfg.AssetID = a.AssetID
			cfg.AssetCoords = a.AssetCoords
			deviceMap[deviceID] = cfg
		}
	}
}

// ─── Sensor registry ──────────────────────────────────────────────────────────

// SensorEntry describes one registered sensor loaded from the external sensor
// registry file (sensors.json). sensor_type is the single source of truth for
// what a sensor measures — device_channels.json references SensorID but never
// repeats SensorType, so there is exactly one place to look it up.
type SensorEntry struct {
	SensorID   string `json:"sensor_id,omitempty"`
	SensorType string `json:"sensor_type,omitempty"`
	DeviceID   string `json:"device_id,omitempty"` // FK -> devices.json; who currently reports this sensor
}

type SensorRegistryFile struct {
	Version string        `json:"version,omitempty"`
	Sensors []SensorEntry `json:"sensors"`
}

// SensorConfig is the resolved projection of one SensorEntry, keyed by
// sensor_id in the map GetSensorsMap returns.
type SensorConfig struct {
	SensorType string
	DeviceID   string
}

// validateSensorEntry checks mandatory fields and format constraints for one
// sensor. validTypes is record.AllSensorTypes(), computed once by the caller
// rather than per-entry. Returns an error for hard failures.
func validateSensorEntry(s SensorEntry, validTypes map[string]bool) error {
	if s.SensorID == "" {
		return fmt.Errorf("sensor_id is required")
	}
	if !isUUIDv7(s.SensorID) {
		return fmt.Errorf("sensor %s: sensor_id must be a valid UUIDv7", s.SensorID)
	}
	if s.SensorType == "" {
		return fmt.Errorf("sensor %s: sensor_type is required", s.SensorID)
	}
	if !validTypes[s.SensorType] {
		return fmt.Errorf("sensor %s: sensor_type %q is not a recognized sensor_type (see record.SensorTypes) — add it there first if it's genuinely new", s.SensorID, s.SensorType)
	}
	if s.DeviceID == "" {
		return fmt.Errorf("sensor %s: device_id is required", s.SensorID)
	}
	if !isUUIDv7(s.DeviceID) {
		return fmt.Errorf("sensor %s: device_id must be a valid UUIDv7", s.SensorID)
	}
	return nil
}

// GetSensorsMap loads the canonical sensor registry from a JSON file.
// Returns sensorID -> SensorConfig. All mandatory fields are validated; the
// whole file is rejected on any error. Cross-file references (device_id
// existing in devices.json) are deliberately NOT checked here — same
// decoupling as GetAssetsMap: an entry pointing at a device_id that doesn't
// (yet) exist just never resolves to anything at decode time, rather than
// failing the whole file. Not unique on (device_id, sensor_type) — two
// sensors of the same type can legitimately share a device (e.g. two
// identical instruments on one RS485 bus); only sensor_id itself is unique.
func GetSensorsMap(registryPath string) (map[string]SensorConfig, error) {
	contents, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, fmt.Errorf("read sensor registry file: %w", err)
	}

	var reg SensorRegistryFile
	if err := json.Unmarshal(contents, &reg); err != nil {
		return nil, fmt.Errorf("parse sensor registry file: %w", err)
	}

	if reg.Version == "" {
		log.Printf("[registry] warning: sensors.json has no version field")
	} else {
		log.Printf("[registry] version=%s sensors=%d", reg.Version, len(reg.Sensors))
	}

	validTypes := record.AllSensorTypes()
	sensorMap := make(map[string]SensorConfig, len(reg.Sensors))

	for i, s := range reg.Sensors {
		if err := validateSensorEntry(s, validTypes); err != nil {
			return nil, fmt.Errorf("entry[%d]: %w", i, err)
		}
		if _, exists := sensorMap[s.SensorID]; exists {
			return nil, fmt.Errorf("duplicate sensor_id in registry: %s", s.SensorID)
		}
		sensorMap[s.SensorID] = SensorConfig{SensorType: s.SensorType, DeviceID: s.DeviceID}
	}

	return sensorMap, nil
}

// ─── Device channel registry (UC100/UC300 RS485 routing) ─────────────────────

// DeviceChannelEntry describes one wire-level channel routing binding for a
// UC-series device: which sensor_id a specific Modbus channel or GPIO number
// currently represents. Exactly one of (ModbusChannel+ModbusSlaveID) or
// GPIONumber must be set — never both, never neither. Never carries
// sensor_type — that lives exclusively in sensors.json (see SensorEntry).
type DeviceChannelEntry struct {
	DeviceID      string `json:"device_id,omitempty"`
	SensorID      string `json:"sensor_id,omitempty"`
	ModbusChannel int    `json:"modbus_channel,omitempty"`  // 1-32; protocol never uses 0
	ModbusSlaveID int    `json:"modbus_slave_id,omitempty"` // RS485 slave address; documentation only, not used at decode time
	// IOChannel is UC300's raw fixed-hardware channel_id byte (3-14),
	// covering GPIO input/output, PT100, and ADC current/voltage alike —
	// they share one channel_id namespace on the wire, so one field covers
	// all of them. UC100 has none of this hardware. Protocol never uses 0.
	IOChannel int `json:"io_channel,omitempty"`
}

type DeviceChannelRegistryFile struct {
	Version  string               `json:"version,omitempty"`
	Channels []DeviceChannelEntry `json:"channels"`
}

// DeviceChannels holds one UC-series device's channel->sensor_id routing,
// split by channel kind since Modbus and IO channel numbers are independent
// namespaces (modbus_channel=3 and io_channel=3 are unrelated channels).
type DeviceChannels struct {
	Modbus map[int]string // modbus_channel -> sensor_id
	IO     map[int]string // io_channel -> sensor_id
}

// validateDeviceChannelEntry checks mandatory fields, format, and the
// modbus-xor-io shape for one entry. deviceMap is used only to enforce that
// a uc100 device never carries an IO entry (uc100 has no fixed-hardware
// GPIO/PT100/ADC channels); when e.DeviceID isn't found in deviceMap that
// check is simply skipped — same "not yet resolvable" leniency as elsewhere
// in this file, not a validation failure.
func validateDeviceChannelEntry(e DeviceChannelEntry, deviceMap map[string]DeviceConfig) error {
	if e.DeviceID == "" {
		return fmt.Errorf("device_id is required")
	}
	if !isUUIDv7(e.DeviceID) {
		return fmt.Errorf("device_id %q must be a valid UUIDv7", e.DeviceID)
	}
	if e.SensorID == "" {
		return fmt.Errorf("device %s: sensor_id is required", e.DeviceID)
	}
	if !isUUIDv7(e.SensorID) {
		return fmt.Errorf("device %s: sensor_id %q must be a valid UUIDv7", e.DeviceID, e.SensorID)
	}

	isModbus := e.ModbusChannel != 0 || e.ModbusSlaveID != 0
	isIO := e.IOChannel != 0

	switch {
	case isModbus && isIO:
		return fmt.Errorf("device %s sensor %s: entry cannot be both a Modbus and an IO channel", e.DeviceID, e.SensorID)
	case !isModbus && !isIO:
		return fmt.Errorf("device %s sensor %s: entry must set either modbus_channel+modbus_slave_id or io_channel", e.DeviceID, e.SensorID)
	case isModbus && (e.ModbusChannel == 0 || e.ModbusSlaveID == 0):
		return fmt.Errorf("device %s sensor %s: modbus_channel and modbus_slave_id must both be set together", e.DeviceID, e.SensorID)
	}

	if isIO {
		if cfg, ok := deviceMap[e.DeviceID]; ok && strings.EqualFold(string(cfg.Model), string(UC100)) {
			return fmt.Errorf("device %s sensor %s: device_model uc100 has no fixed I/O hardware, but an io_channel entry was given", e.DeviceID, e.SensorID)
		}
	}

	return nil
}

// GetDeviceChannelsMap loads the UC-series channel routing registry from a
// JSON file. Returns deviceID -> DeviceChannels. Routing only — never
// carries sensor_type. device_id/sensor_id existence in devices.json/
// sensors.json is deliberately not checked here, same decoupling as
// GetAssetsMap/GetSensorsMap — an entry referencing something that doesn't
// exist yet just never resolves to a tag at decode time. A channel number
// (Modbus or IO) reused across two entries on the same device IS a hard
// error, since that's a real, structural misconfiguration, not a timing gap.
func GetDeviceChannelsMap(registryPath string, deviceMap map[string]DeviceConfig) (map[string]DeviceChannels, error) {
	contents, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, fmt.Errorf("read device channel registry file: %w", err)
	}

	var reg DeviceChannelRegistryFile
	if err := json.Unmarshal(contents, &reg); err != nil {
		return nil, fmt.Errorf("parse device channel registry file: %w", err)
	}

	if reg.Version == "" {
		log.Printf("[registry] warning: device_channels.json has no version field")
	} else {
		log.Printf("[registry] version=%s channels=%d", reg.Version, len(reg.Channels))
	}

	out := make(map[string]DeviceChannels)
	seenSensorID := make(map[string]bool, len(reg.Channels))

	for i, e := range reg.Channels {
		if err := validateDeviceChannelEntry(e, deviceMap); err != nil {
			return nil, fmt.Errorf("entry[%d]: %w", i, err)
		}
		if seenSensorID[e.SensorID] {
			return nil, fmt.Errorf("entry[%d]: duplicate sensor_id in device channel registry: %s", i, e.SensorID)
		}
		seenSensorID[e.SensorID] = true

		dc, ok := out[e.DeviceID]
		if !ok {
			dc = DeviceChannels{Modbus: make(map[int]string), IO: make(map[int]string)}
		}
		if e.ModbusChannel != 0 {
			if existing, exists := dc.Modbus[e.ModbusChannel]; exists {
				return nil, fmt.Errorf("entry[%d]: device %s: modbus_channel %d already assigned to sensor %s", i, e.DeviceID, e.ModbusChannel, existing)
			}
			dc.Modbus[e.ModbusChannel] = e.SensorID
		} else {
			if existing, exists := dc.IO[e.IOChannel]; exists {
				return nil, fmt.Errorf("entry[%d]: device %s: io_channel %d already assigned to sensor %s", i, e.DeviceID, e.IOChannel, existing)
			}
			dc.IO[e.IOChannel] = e.SensorID
		}
		out[e.DeviceID] = dc
	}

	return out, nil
}

// sensorIndexKey joins deviceID+sensorType into the reverse-index key used by
// buildSensorIndex/resolveSensorID. Not exported; the separator can't appear
// in a UUIDv7 or a record.ST value, so a plain join is safe.
func sensorIndexKey(deviceID, sensorType string) string {
	return deviceID + "\x00" + sensorType
}

// buildSensorIndex derives a (device_id, sensor_type) -> sensor_id lookup from
// sensors.json, used to stamp a stable sensor_id onto every decoded record
// without decoders needing to know about the sensor registry at all.
//
// sensors.json is deliberately NOT unique on (device_id, sensor_type) — e.g.
// two identical Modbus instruments on the same UC300 bus can share a
// sensor_type. Such pairs are ambiguous from sensor_type alone, so they are
// left out of this index (logged, not an error); resolving them requires
// channel-aware lookup via device_channels.json, done separately inside the
// UC-series decoders once those exist.
func buildSensorIndex(sensorMap map[string]SensorConfig) map[string]string {
	counts := make(map[string]int, len(sensorMap))
	sensorIDByKey := make(map[string]string, len(sensorMap))
	for sensorID, s := range sensorMap {
		key := sensorIndexKey(s.DeviceID, s.SensorType)
		counts[key]++
		sensorIDByKey[key] = sensorID
	}
	index := make(map[string]string, len(sensorIDByKey))
	for key, sensorID := range sensorIDByKey {
		if counts[key] > 1 {
			log.Printf("[registry] warning: device_id/sensor_type pair is ambiguous in sensors.json (%d sensors share it) — skipping from sensor_id index: %s", counts[key], key)
			continue
		}
		index[key] = sensorID
	}
	return index
}

// ─────────────────────────────────────────────────────────────────────────────
// Provider detection
// ─────────────────────────────────────────────────────────────────────────────

// lnsProviderProbe captures the minimum root-level fields needed to fingerprint
// an LNS message. json.Unmarshal silently ignores all other fields.
type lnsProviderProbe struct {
	DeduplicationId string `json:"deduplicationId"` // ChirpstackV4
	Meta            struct {
		Device string `json:"device"` // Everynet
	} `json:"meta"`
	Params struct {
		Payload string `json:"payload"` // Everynet
	} `json:"params"`
}

// detectProvider identifies the message source from JSON content alone.
// Returns "chirpstackv4", "everynet", "custom", or "" (unknown).
func detectProvider(message string) string {
	var lns lnsProviderProbe
	if json.Unmarshal([]byte(message), &lns) == nil {
		if lns.DeduplicationId != "" {
			return "chirpstackv4"
		}
		if lns.Meta.Device != "" && lns.Params.Payload != "" {
			return "everynet"
		}
	}
	// Custom direct-MQTT: JSON array with variable:"data"
	var probe []struct {
		Variable string `json:"variable"`
	}
	if json.Unmarshal([]byte(message), &probe) == nil && len(probe) > 0 && probe[0].Variable == "data" {
		return "custom"
	}
	return ""
}

// ─────────────────────────────────────────────────────────────────────────────
// Colored logging helpers
// ─────────────────────────────────────────────────────────────────────────────

// ANSI color codes. logError prints in bold red; logWarn in bold yellow.
// Both write to the same destination as log.Printf so timestamps align.
func logError(format string, args ...any) {
	log.Printf("\033[1;31m[ERROR] "+format+"\033[0m", args...)
}
func logWarn(format string, args ...any) {
	log.Printf("\033[1;33m[WARN]  "+format+"\033[0m", args...)
}

// ─────────────────────────────────────────────────────────────────────────────
// InfluxDB3 writer
// ─────────────────────────────────────────────────────────────────────────────

// validateRecord returns an error if the record would produce a malformed or
// unqueryable point in InfluxDB3.  Checked before building the Point object so
// bad records are rejected at zero network cost.
func validateRecord(r record.SensorDataRecord) error {
	switch {
	case r.SensorType == "":
		return fmt.Errorf("empty sensor_type (device_id=%s)", r.DeviceID)
	case r.DeviceID == "":
		return fmt.Errorf("empty device_id (sensor_type=%s)", r.SensorType)
	case r.DeviceModel == "":
		return fmt.Errorf("empty device_model (device_id=%s, sensor_type=%s)", r.DeviceID, r.SensorType)
	case r.Timestamp.IsZero():
		return fmt.Errorf("zero timestamp (device_id=%s, sensor_type=%s)", r.DeviceID, r.SensorType)
	case r.ValueType != "float" && r.ValueType != "int" && r.ValueType != "bool":
		return fmt.Errorf("unknown value_type %q (device_id=%s, sensor_type=%s)", r.ValueType, r.DeviceID, r.SensorType)
	case r.ValueType == "float" && r.ValueFloat == nil:
		return fmt.Errorf("nil float value (device_id=%s, sensor_type=%s)", r.DeviceID, r.SensorType)
	case r.ValueType == "float" && r.ValueFloat != nil && (math.IsNaN(*r.ValueFloat) || math.IsInf(*r.ValueFloat, 0)):
		return fmt.Errorf("non-finite float %v (device_id=%s, sensor_type=%s)", *r.ValueFloat, r.DeviceID, r.SensorType)
	case r.ValueType == "int" && r.ValueInt == nil:
		return fmt.Errorf("nil int value (device_id=%s, sensor_type=%s)", r.DeviceID, r.SensorType)
	case r.ValueType == "bool" && r.ValueBool == nil:
		return fmt.Errorf("nil bool value (device_id=%s, sensor_type=%s)", r.DeviceID, r.SensorType)
	}
	return nil
}

func writeSensorRecords(ctx context.Context, iot *influxdb3.Client, records []record.SensorDataRecord) error {
	if len(records) == 0 {
		return nil
	}
	points := make([]*influxdb3.Point, 0, len(records))
	for _, r := range records {
		if err := validateRecord(r); err != nil {
			logWarn("dropping record: %v", err)
			continue
		}
		p := influxdb3.NewPointWithMeasurement("sensor_data").
			SetTag("sensor_type", r.SensorType).
			SetTag("device_model", r.DeviceModel).
			SetTag("device_id", r.DeviceID).
			SetTag("provider", r.Provider).
			SetTimestamp(r.Timestamp)
		if r.DevEUI != "" {
			p = p.SetTag("dev_eui", r.DevEUI)
		}
		if r.MacAddress != "" {
			p = p.SetTag("mac_address", r.MacAddress)
		}
		if r.SensorID != "" {
			p = p.SetTag("sensor_id", r.SensorID)
		}
		switch r.ValueType {
		case "float":
			if r.ValueFloat != nil {
				p = p.SetDoubleField("value_float", *r.ValueFloat)
			}
		case "int":
			if r.ValueInt != nil {
				p = p.SetIntegerField("value_int", *r.ValueInt)
			}
		case "bool":
			if r.ValueBool != nil {
				p = p.SetBooleanField("value_bool", *r.ValueBool)
			}
		}
		if p.HasFields() {
			points = append(points, p)
		}
	}
	if len(points) == 0 {
		return nil
	}
	// NoSync avoids WAL-lock conflicts on InfluxDB3 Core under concurrent writes.
	// For IoT sensor data losing a point on a hard crash is acceptable.
	const maxAttempts = 3
	batchErr := writeWithRetry(ctx, iot, points, maxAttempts)
	if batchErr == nil {
		return nil
	}
	// Batch failed — fall back to per-point writes so one bad record does not
	// silently drop all other sensor readings from the same message.
	logWarn("batch of %d points failed (%v) – retrying individually", len(points), batchErr)
	var dropped int
	for i, pt := range points {
		if err := writeWithRetry(ctx, iot, []*influxdb3.Point{pt}, maxAttempts); err != nil {
			// Include the tag values so the bad point is identifiable in logs.
			devID, _ := pt.GetTag("device_id")
			sensorType, _ := pt.GetTag("sensor_type")
			logError("point %d/%d dropped permanently: device_id=%s sensor_type=%s error=%v",
				i+1, len(points), devID, sensorType, err)
			dropped++
		}
	}
	if dropped > 0 {
		logError("%d/%d points dropped for this message", dropped, len(points))
		return fmt.Errorf("%d points failed to write", dropped)
	}
	return nil
}

// writeWithRetry attempts to write points up to maxAttempts times with
// exponential back-off. Unrecoverable 4xx errors (except 429) are returned
// immediately without retrying.
func writeWithRetry(ctx context.Context, iot *influxdb3.Client, points []*influxdb3.Point, maxAttempts int) error {
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := iot.WritePoints(ctx, points, influxdb3.WithNoSync(true))
		if err == nil {
			return nil
		}
		errStr := err.Error()
		// 4xx errors other than 429 (rate limit) are not recoverable – return immediately.
		if is4xx(errStr) && !strings.Contains(errStr, "429") {
			if strings.Contains(errStr, "404") {
				logError("write rejected with 404 — database may not exist in InfluxDB3 (create it first): %v", err)
			} else {
				logError("write rejected by InfluxDB3 (unrecoverable %s): %v", extractHTTPStatus(errStr), err)
			}
			return err
		}
		if is5xxTransient(errStr) {
			if attempt < maxAttempts {
				backoff := time.Duration(attempt*attempt) * 250 * time.Millisecond
				logWarn("write attempt %d/%d: InfluxDB3 unreachable (proxy/server down: %v) — retrying in %v", attempt, maxAttempts, err, backoff)
				time.Sleep(backoff)
				continue
			}
			logError("write failed after %d attempts — InfluxDB3 unreachable: %v", maxAttempts, err)
			return err
		}
		if attempt < maxAttempts {
			backoff := time.Duration(attempt*attempt) * 250 * time.Millisecond
			logWarn("write attempt %d/%d failed (%v) — retrying in %v", attempt, maxAttempts, err, backoff)
			time.Sleep(backoff)
		} else {
			logError("write failed after %d attempts: %v", maxAttempts, err)
			return err
		}
	}
	return nil // unreachable
}

// is4xx reports whether an error string contains an unrecoverable HTTP 4xx status code.
// 429 (rate limit) is excluded — it is transient and should be retried.
func is4xx(errStr string) bool {
	for _, code := range []string{"400", "401", "403", "404", "405", "422"} {
		if strings.Contains(errStr, code) {
			return true
		}
	}
	return false
}

// is5xxTransient reports HTTP 5xx errors that are worth retrying (proxy/server down).
func is5xxTransient(errStr string) bool {
	for _, s := range []string{"502", "503", "Bad Gateway", "Service Unavailable"} {
		if strings.Contains(errStr, s) {
			return true
		}
	}
	return false
}

// extractHTTPStatus returns the first HTTP status code found in errStr, or "4xx".
func extractHTTPStatus(errStr string) string {
	for _, code := range []string{"400", "401", "403", "404", "405", "422"} {
		if strings.Contains(errStr, code) {
			return code
		}
	}
	return "4xx"
}

// writeAuditLog writes the raw MQTT payload to the audit_log measurement.
// Tag:   device_id  (low cardinality — one per physical device)
// Field: raw_data   (string — original message, truncated to 512 chars)
// raw_data is a FIELD (not a tag) to avoid high cardinality in InfluxDB3.
// writeAuditLog writes every raw MQTT/LoRaWAN payload to the audit_iot database.
//
// Schema (measurement = "raw"):
//
//	Tags:  device_id                   — identifies the physical device
//	       event_type = "payload_ingest" — fixed; low-cardinality; extensible
//	Field: raw_data  (string ≤512 chars) — original undecoded message
//	Time:  ingestion timestamp (time.Now() at message receipt)
//
// The audit database carries a short retention policy set on the InfluxDB3 server
// (e.g., 7–30 days) independently of the sensor_data database.
func writeAuditLog(ctx context.Context, audit *influxdb3.Client, deviceID, rawData string, ts time.Time) {
	const maxLen = 512
	if len(rawData) > maxLen {
		rawData = rawData[:maxLen] + "…"
	}
	p := influxdb3.NewPointWithMeasurement("raw").
		SetTag("device_id", deviceID).
		SetTag("event_type", "payload_ingest").
		SetStringField("raw_data", rawData).
		SetTimestamp(ts)
	if err := writeWithRetry(ctx, audit, []*influxdb3.Point{p}, 3); err != nil {
		logWarn("audit write failed for device_id=%s: %v", deviceID, err)
	}
}

// writeCalibrations writes sensor_calibration entries for every explicit
// DeviceCalibration defined in the device registry.
// Called once at startup; entries act as a reference table for users plotting
// calibrated values with: calibrated = (raw ^ power) × scale + offset
// fetchLatestCalibrations reads the most recent sensor_calibration row per
// (device_id, sensor_type) key, so writeCalibrations can skip re-writing a
// row whose values already match — otherwise every process restart appends a
// full duplicate set of rows, which degrades the read-time run-length
// encoding the timeseries API relies on (api/src/routes/timeseries.ts
// findApplicableCalibration/calibrationChanged: calibration fields are meant
// to appear once per stream, not once per restart).
//
// Returns an empty map (not an error) when sensor_calibration has no rows
// yet — e.g. the very first run against a fresh database.
func fetchLatestCalibrations(ctx context.Context, iot *influxdb3.Client) map[string]DeviceCalibration {
	existing := make(map[string]DeviceCalibration)

	it, err := iot.QueryPointValue(ctx, `
		SELECT device_id, sensor_type, "scale", "offset", "power"
		FROM sensor_calibration
		ORDER BY time DESC
	`)
	if err != nil {
		log.Printf("[calibration] no existing sensor_calibration rows to diff against (likely first run): %v", err)
		return existing
	}

	for {
		pv, nextErr := it.Next()
		if nextErr != nil {
			if nextErr != influxdb3.Done {
				log.Printf("[calibration] error reading existing sensor_calibration rows: %v", nextErr)
			}
			break
		}
		deviceID, ok := pv.GetTag("device_id")
		if !ok {
			continue
		}
		sensorType, ok := pv.GetTag("sensor_type")
		if !ok {
			continue
		}
		// Rows arrive newest-first; keep only the first (most recent) one seen per key.
		key := sensorIndexKey(deviceID, sensorType)
		if _, seen := existing[key]; seen {
			continue
		}
		scale, offset, power := pv.GetDoubleField("scale"), pv.GetDoubleField("offset"), pv.GetDoubleField("power")
		if scale == nil || offset == nil || power == nil {
			continue
		}
		existing[key] = DeviceCalibration{SensorType: sensorType, Scale: *scale, Offset: *offset, Power: *power}
	}

	return existing
}

func writeCalibrations(ctx context.Context, iot *influxdb3.Client, deviceMap map[string]DeviceConfig) {
	existing := fetchLatestCalibrations(ctx, iot)

	var points []*influxdb3.Point
	unchanged := 0
	ts := time.Now()
	for deviceID, config := range deviceMap {
		for _, c := range config.Calibrations {
			if prev, ok := existing[sensorIndexKey(deviceID, c.SensorType)]; ok && prev == c {
				unchanged++
				continue
			}
			p := influxdb3.NewPointWithMeasurement("sensor_calibration").
				SetTag("device_id", deviceID).
				SetTag("sensor_type", c.SensorType).
				SetDoubleField("scale", c.Scale).
				SetDoubleField("offset", c.Offset).
				SetDoubleField("power", c.Power).
				SetTimestamp(ts)
			points = append(points, p)
		}
	}
	if len(points) == 0 {
		if unchanged > 0 {
			log.Printf("[calibration] all %d calibrations already match the latest sensor_calibration rows — nothing to write", unchanged)
		} else {
			log.Printf("[calibration] no custom calibrations defined — all devices use identity transform")
		}
		return
	}
	if err := iot.WritePoints(ctx, points, influxdb3.WithNoSync(true)); err != nil {
		logError("calibration write failed: %v", err)
	} else {
		log.Printf("[calibration] wrote %d entries to sensor_calibration (%d unchanged, skipped)", len(points), unchanged)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Payload dispatcher
// ─────────────────────────────────────────────────────────────────────────────

func b64ToByte(b64 string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(b64)
}

// registrySnapshot bundles the hot-reloadable sensor/channel lookup maps a
// single decode+write needs. Workers capture one atomically under
// registryMu.RLock per message so a mid-flight reload can't mix an old
// sensorIndex with a new deviceChannels map.
type registrySnapshot struct {
	SensorIndex    map[string]string
	SensorMap      map[string]SensorConfig
	DeviceChannels map[string]DeviceChannels
}

// resolveChannelRecord fills in SensorType/SensorID for a UC100/UC300 record
// carrying ModbusChannel or IOChannel instead of SensorType (see
// record.SensorDataRecord) by looking channel up in channelMap (either
// reg.DeviceChannels[deviceID].Modbus or .IO). kind is "modbus" or "io", used
// only for log messages. Returns ok=false when the channel has no
// device_channels.json entry, or that entry's sensor_id has no matching
// sensors.json entry — such readings are dropped by the caller rather than
// written under an empty/undefined sensor_type.
func resolveChannelRecord(r *record.SensorDataRecord, deviceID string, channel int, channelMap map[int]string, reg registrySnapshot, kind string) bool {
	sensorID, ok := channelMap[channel]
	if !ok {
		log.Printf("[parse] device_id=%s %s_channel=%d has no device_channels.json entry — dropping unmapped reading", deviceID, kind, channel)
		return false
	}
	sensor, ok := reg.SensorMap[sensorID]
	if !ok || sensor.SensorType == "" {
		log.Printf("[parse] device_id=%s %s_channel=%d sensor_id=%s not found in sensors.json — dropping unmapped reading", deviceID, kind, channel, sensorID)
		return false
	}
	r.SensorID = sensorID
	r.SensorType = sensor.SensorType
	return true
}

// parseDeviceModel decodes rawPayload and writes sensor_data points to InfluxDB3.
//
// provider == "custom": rawPayload is raw JSON; timestamp comes from the message.
// LNS providers:        rawPayload is base64-encoded binary; timestamp from LNS frame.
func parseDeviceModel(ctx context.Context, iot *influxdb3.Client,
	deviceID, devEUI string, config DeviceConfig, provider, rawPayload string, port uint64, ts time.Time,
	reg registrySnapshot) {

	if rawPayload == "" {
		log.Printf("[parse] empty payload for deviceID=%s", deviceID)
		return
	}

	var records []record.SensorDataRecord

	deviceModel := config.Model
	if provider == "custom" {
		// Custom path: raw JSON, decoder extracts its own timestamp.
		var ok bool
		records, ok = registry.ParseCustom(string(deviceModel), "", rawPayload, deviceID)
		if !ok {
			log.Printf("[parse] no custom decoder for device model %s", deviceModel)
			return
		}
	} else {
		// LNS path: base64-encoded binary payload.
		b, err := b64ToByte(rawPayload)
		if err != nil {
			log.Printf("[parse] b64 decode error for %s: %v", deviceID, err)
			return
		}
		var ok bool
		records, ok = registry.DecodeLNS(string(deviceModel), "", b, deviceID, provider, port, ts)
		if !ok {
			log.Printf("[parse] no LNS decoder for device model %s", deviceModel)
			return
		}
	}

	if len(records) == 0 {
		log.Printf("[parse] decoder returned 0 records for %s (%s)", deviceID, deviceModel)
		return
	}
	resolved := records[:0]
	for i := range records {
		r := records[i]
		r.DevEUI = devEUI
		r.MacAddress = config.MacAddress
		switch {
		case r.ModbusChannel != 0:
			if !resolveChannelRecord(&r, deviceID, r.ModbusChannel, reg.DeviceChannels[deviceID].Modbus, reg, "modbus") {
				continue
			}
		case r.IOChannel != 0:
			if !resolveChannelRecord(&r, deviceID, r.IOChannel, reg.DeviceChannels[deviceID].IO, reg, "io") {
				continue
			}
		default:
			if sensorID, ok := reg.SensorIndex[sensorIndexKey(deviceID, r.SensorType)]; ok {
				r.SensorID = sensorID
			}
		}
		resolved = append(resolved, r)
	}
	records = resolved
	if len(records) == 0 {
		log.Printf("[parse] all records dropped after sensor resolution for %s (%s)", deviceID, deviceModel)
		return
	}
	if err := writeSensorRecords(ctx, iot, records); err != nil {
		logError("sensor write failed for device_id=%s model=%s: %v", deviceID, deviceModel, err)
	} else {
		if devEUI != "" {
			log.Printf("[influxdb3] wrote %d points: device_model=%s device_id=%s dev_eui=%s",
				len(records), strings.ToLower(string(deviceModel)), deviceID, devEUI)
		} else {
			log.Printf("[influxdb3] wrote %d points: device_model=%s device_id=%s",
				len(records), strings.ToLower(string(deviceModel)), deviceID)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Message entry point – single provider switch, no redundant checks
// ─────────────────────────────────────────────────────────────────────────────

// parseMsg routes an incoming MQTT message to the correct decoder.
func parseMsg(ctx context.Context, iot *influxdb3.Client, provider, deviceID, devEUI string, config DeviceConfig, message string, reg registrySnapshot) {
	if message == "" {
		return
	}
	switch provider {

	case "custom":
		// Direct MQTT device: raw JSON, no LNS frame wrapping.
		parseDeviceModel(ctx, iot, deviceID, "", config, "custom", message, 0, time.Time{}, reg)

	case "chirpstackv4":
		frame, err := chirpstack.Parse(message)
		if err != nil {
			log.Printf("[parseMsg/chirpstackv4] %v (deviceID=%s)", err, deviceID)
			return
		}
		parseDeviceModel(ctx, iot, deviceID, devEUI, config, "chirpstackv4", frame.Data, frame.Port, frame.Timestamp, reg)

	case "everynet":
		frame, err := everynet.Parse(message)
		if err != nil {
			log.Printf("[parseMsg/everynet] %v (deviceID=%s)", err, deviceID)
			return
		}
		parseDeviceModel(ctx, iot, deviceID, devEUI, config, "everynet", frame.Data, frame.Port, frame.Timestamp, reg)

	default:
		log.Printf("[parseMsg] unsupported provider=%s for device_id=%s (%.80s…)", provider, deviceID, message)
	}
}

// resolveIncomingDevice maps topic identifier to canonical deviceID.
// For custom provider, topic contains deviceID directly.
// For LoRaWAN providers, topic contains devEUI and must be resolved via registry map.
func resolveIncomingDevice(provider, topicIdentifier string, deviceMap map[string]DeviceConfig, devEUIToDeviceID map[string]string) (deviceID, devEUI string, config DeviceConfig, ok bool) {
	if provider == "custom" {
		deviceID = topicIdentifier
		config, ok = deviceMap[deviceID]
		return deviceID, "", config, ok
	}

	if provider != "chirpstackv4" && provider != "everynet" {
		return "", "", DeviceConfig{}, false
	}

	devEUI = strings.ToLower(topicIdentifier)
	deviceID, ok = devEUIToDeviceID[devEUI]
	if !ok {
		return "", devEUI, DeviceConfig{}, false
	}
	config, ok = deviceMap[deviceID]
	if !ok {
		return deviceID, devEUI, DeviceConfig{}, false
	}
	return deviceID, devEUI, config, true
}

// ─────────────────────────────────────────────────────────────────────────────
// MQTT handler + main
// ─────────────────────────────────────────────────────────────────────────────

func connLostHandler(c MQTT.Client, err error) {
	fmt.Printf("MQTT connection lost: %v\n", err)
	os.Exit(1)
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found; using environment variables")
	}

	MQTT_BROKER := os.Getenv("MQTT_BROKER")

	INFLUXDB_HOST := os.Getenv("INFLUXDB_HOST")
	if INFLUXDB_HOST == "" {
		INFLUXDB_HOST = "http://influxdb.maua.br:8181"
	}
	INFLUXDB_TOKEN := os.Getenv("INFLUXDB_TOKEN")
	DEVICE_REGISTRY_FILE := os.Getenv("DEVICE_REGISTRY_FILE")
	if DEVICE_REGISTRY_FILE == "" {
		DEVICE_REGISTRY_FILE = "devices.json"
	}
	ASSET_REGISTRY_FILE := os.Getenv("ASSET_REGISTRY_FILE")
	if ASSET_REGISTRY_FILE == "" {
		ASSET_REGISTRY_FILE = "assets.json"
	}
	SENSOR_REGISTRY_FILE := os.Getenv("SENSOR_REGISTRY_FILE")
	if SENSOR_REGISTRY_FILE == "" {
		SENSOR_REGISTRY_FILE = "sensors.json"
	}
	DEVICE_CHANNEL_REGISTRY_FILE := os.Getenv("DEVICE_CHANNEL_REGISTRY_FILE")
	if DEVICE_CHANNEL_REGISTRY_FILE == "" {
		DEVICE_CHANNEL_REGISTRY_FILE = "device_channels.json"
	}
	DEVICE_REGISTRY_REFRESH_SEC := 30
	if raw := os.Getenv("DEVICE_REGISTRY_REFRESH_SEC"); raw != "" {
		if v, convErr := strconv.Atoi(raw); convErr == nil && v > 0 {
			DEVICE_REGISTRY_REFRESH_SEC = v
		}
	}

	ctx := context.Background()

	// Initialize all four InfluxDB3 clients on the same host+token.
	// Database names are hardcoded; each client writes to a different database.
	var clients DBClients
	var err error

	// Create iot_sensors client
	clients.IoTSensors, err = influxdb3.New(influxdb3.ClientConfig{
		Host:     INFLUXDB_HOST,
		Token:    INFLUXDB_TOKEN,
		Database: "iot_sensors",
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create iot_sensors client: %v", err))
	}

	// Create audit_iot client
	clients.AuditIoT, err = influxdb3.New(influxdb3.ClientConfig{
		Host:     INFLUXDB_HOST,
		Token:    INFLUXDB_TOKEN,
		Database: "audit_iot",
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create audit_iot client: %v", err))
	}

	// Create vehicle_telemetry client
	clients.VehicleTelemetry, err = influxdb3.New(influxdb3.ClientConfig{
		Host:     INFLUXDB_HOST,
		Token:    INFLUXDB_TOKEN,
		Database: "vehicle_telemetry",
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create vehicle_telemetry client: %v", err))
	}

	// Create audit_vehicle client
	clients.AuditVehicle, err = influxdb3.New(influxdb3.ClientConfig{
		Host:     INFLUXDB_HOST,
		Token:    INFLUXDB_TOKEN,
		Database: "audit_vehicle",
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create audit_vehicle client: %v", err))
	}

	defer clients.Close()
	log.Printf("InfluxDB3: host=%s databases=[iot_sensors, audit_iot, vehicle_telemetry, audit_vehicle] (all on same host+token)", INFLUXDB_HOST)

	deviceMap, devEUIToDeviceID, err := GetDevicesMap(DEVICE_REGISTRY_FILE)
	if err != nil {
		panic(fmt.Sprintf("failed to load device registry (%s): %v", DEVICE_REGISTRY_FILE, err))
	}
	log.Printf("Device registry: file=%s deviceIDs=%d devEUI mappings=%d", DEVICE_REGISTRY_FILE, len(deviceMap), len(devEUIToDeviceID))

	// Asset registry is supplementary metadata (no decoder depends on it), so
	// a bad/missing assets.json logs a warning and starts with no asset links
	// rather than crashing the service like a bad devices.json would.
	assetByDeviceID, assetErr := GetAssetsMap(ASSET_REGISTRY_FILE)
	if assetErr != nil {
		log.Printf("[registry] warning: failed to load asset registry (%s): %v", ASSET_REGISTRY_FILE, assetErr)
		assetByDeviceID = map[string]AssetConfig{}
	}
	mergeAssetInfo(deviceMap, assetByDeviceID)
	log.Printf("Asset registry: file=%s deviceLinks=%d", ASSET_REGISTRY_FILE, len(assetByDeviceID))

	// Sensor and device-channel registries are supplementary metadata (no
	// decoder depends on them to produce a value — only to tag it with a
	// stable sensor_id), so a bad/missing file logs a warning and starts
	// with empty maps rather than crashing the service, mirroring assets.json.
	sensorMap, sensorErr := GetSensorsMap(SENSOR_REGISTRY_FILE)
	if sensorErr != nil {
		log.Printf("[registry] warning: failed to load sensor registry (%s): %v", SENSOR_REGISTRY_FILE, sensorErr)
		sensorMap = map[string]SensorConfig{}
	}
	deviceChannels, channelErr := GetDeviceChannelsMap(DEVICE_CHANNEL_REGISTRY_FILE, deviceMap)
	if channelErr != nil {
		log.Printf("[registry] warning: failed to load device channel registry (%s): %v", DEVICE_CHANNEL_REGISTRY_FILE, channelErr)
		deviceChannels = map[string]DeviceChannels{}
	}
	sensorIndex := buildSensorIndex(sensorMap)
	log.Printf("Sensor registry: file=%s sensors=%d index=%d", SENSOR_REGISTRY_FILE, len(sensorMap), len(sensorIndex))
	log.Printf("Device channel registry: file=%s devicesWithChannels=%d", DEVICE_CHANNEL_REGISTRY_FILE, len(deviceChannels))

	writeCalibrations(ctx, clients.IoTSensors, deviceMap)

	var registryMu sync.RWMutex
	refreshTicker := time.NewTicker(time.Duration(DEVICE_REGISTRY_REFRESH_SEC) * time.Second)
	defer refreshTicker.Stop()
	go func() {
		for range refreshTicker.C {
			nextDeviceMap, nextDevEUIToDeviceID, reloadErr := GetDevicesMap(DEVICE_REGISTRY_FILE)
			if reloadErr != nil {
				log.Printf("[registry] reload failed: %v", reloadErr)
				continue
			}
			// Keep the last known-good asset links on a transient assets.json
			// read/parse failure, rather than wiping every device's asset
			// assignment just because devices.json happened to reload fine.
			if nextAssetByDeviceID, assetReloadErr := GetAssetsMap(ASSET_REGISTRY_FILE); assetReloadErr != nil {
				log.Printf("[registry] asset reload failed, keeping previous asset links: %v", assetReloadErr)
			} else {
				assetByDeviceID = nextAssetByDeviceID
			}
			mergeAssetInfo(nextDeviceMap, assetByDeviceID)

			// Safe to call on every reload now that writeCalibrations diffs
			// against the latest existing row per key and only writes what
			// actually changed (see fetchLatestCalibrations).
			writeCalibrations(ctx, clients.IoTSensors, nextDeviceMap)

			// Same last-known-good behavior for sensors.json/device_channels.json.
			// Computed into local vars first so deviceChannels/sensorMap/sensorIndex
			// (all read by workers) flip together atomically under registryMu.
			nextSensorMap := sensorMap
			if m, sensorReloadErr := GetSensorsMap(SENSOR_REGISTRY_FILE); sensorReloadErr != nil {
				log.Printf("[registry] sensor registry reload failed, keeping previous sensor index: %v", sensorReloadErr)
			} else {
				nextSensorMap = m
			}
			nextDeviceChannels := deviceChannels
			if m, channelReloadErr := GetDeviceChannelsMap(DEVICE_CHANNEL_REGISTRY_FILE, nextDeviceMap); channelReloadErr != nil {
				log.Printf("[registry] device channel registry reload failed, keeping previous channels: %v", channelReloadErr)
			} else {
				nextDeviceChannels = m
			}
			nextSensorIndex := buildSensorIndex(nextSensorMap)

			registryMu.Lock()
			deviceMap = nextDeviceMap
			devEUIToDeviceID = nextDevEUIToDeviceID
			sensorMap = nextSensorMap
			deviceChannels = nextDeviceChannels
			sensorIndex = nextSensorIndex
			registryMu.Unlock()
			log.Printf("[registry] reloaded: file=%s deviceIDs=%d devEUI mappings=%d assetLinks=%d sensors=%d sensorIndex=%d devicesWithChannels=%d",
				DEVICE_REGISTRY_FILE, len(nextDeviceMap), len(nextDevEUIToDeviceID), len(assetByDeviceID), len(nextSensorMap), len(nextSensorIndex), len(nextDeviceChannels))
		}
	}()

	id := uuid.New().String()
	mqttOpts := MQTT.NewClientOptions().
		AddBroker(MQTT_BROKER).
		SetClientID("parse-lns-sub-" + id).
		SetUsername("public").
		SetPassword("public").
		SetConnectionLostHandler(connLostHandler)

	incoming := make(chan [2]string, 256)
	mqttOpts.SetDefaultPublishHandler(func(_ MQTT.Client, msg MQTT.Message) {
		incoming <- [2]string{msg.Topic(), string(msg.Payload())}
	})

	mqttClient := MQTT.NewClient(mqttOpts)
	if tok := mqttClient.Connect(); tok.Wait() && tok.Error() != nil {
		panic(tok.Error())
	}
	log.Printf("MQTT: connected to %s", MQTT_BROKER)

	// Subscribe to  device/DEVICE_ID/telemetry  or the broader wildcard below.
	subTopic := "device/+/telemetry"
	if tok := mqttClient.Subscribe(subTopic, 0, nil); tok.Wait() && tok.Error() != nil {
		log.Fatal(tok.Error())
	}
	log.Printf("MQTT: subscribed to %s", subTopic)

	// WRITE_WORKERS controls how many goroutines process MQTT messages in parallel.
	// Each worker handles the full decode+write pipeline for one message.
	// Defaults to 2× GOMAXPROCS so InfluxDB3 HTTP latency is hidden behind concurrency.
	numWorkers := runtime.GOMAXPROCS(0) * 2
	if raw := os.Getenv("WRITE_WORKERS"); raw != "" {
		if v, convErr := strconv.Atoi(raw); convErr == nil && v > 0 {
			numWorkers = v
		}
	}
	log.Printf("[main] starting %d worker goroutines", numWorkers)

	// Worker pool ─────────────────────────────────────────────────────────────
	// Each worker drains from the shared incoming channel. The MQTT publish
	// handler never blocks as long as the channel has capacity (256 slots).
	// Workers share read-only access to registry maps (protected by registryMu)
	// and the influxdb3 clients (which are goroutine-safe).
	var workerWG sync.WaitGroup
	for range numWorkers {
		workerWG.Add(1)
		go func() {
			defer workerWG.Done()
			for msg := range incoming {
				topic, payload := msg[0], msg[1]

				// Extract topic identifier from device/IDENTIFIER/telemetry.
				// For custom provider IDENTIFIER is deviceID; for LNS providers it is devEUI.
				parts := strings.SplitN(topic, "/", 3)
				if len(parts) < 2 {
					log.Printf("[main] unexpected topic format: %s", topic)
					continue
				}
				topicIdentifier := parts[1]

				provider := detectProvider(payload)
				if provider == "" {
					registryMu.RLock()
					configFromTopic, exists := deviceMap[topicIdentifier]
					registryMu.RUnlock()
					if exists {
						provider = "custom"
						log.Printf("[provider] fallback to custom for known device_id=%s model=%s", topicIdentifier, configFromTopic.Model)
					} else {
						log.Printf("[drop] cannot detect provider topic=%s", topic)
						continue
					}
				}

				registryMu.RLock()
				currentDeviceMap := deviceMap
				currentDevEUIToDeviceID := devEUIToDeviceID
				currentReg := registrySnapshot{SensorIndex: sensorIndex, SensorMap: sensorMap, DeviceChannels: deviceChannels}
				registryMu.RUnlock()

				deviceID, devEUI, config, known := resolveIncomingDevice(provider, topicIdentifier, currentDeviceMap, currentDevEUIToDeviceID)
				if !known {
					if provider == "custom" {
						log.Printf("[drop] unknown custom device_id=%s topic=%s", topicIdentifier, topic)
					} else {
						log.Printf("[drop] unknown LNS mapping dev_eui=%s provider=%s topic=%s", topicIdentifier, provider, topic)
					}
					continue
				}

				if devEUI != "" {
					log.Printf("[recv] topic=%s provider=%s device_id=%s dev_eui=%s model=%s", topic, provider, deviceID, devEUI, config.Model)
				} else {
					log.Printf("[recv] topic=%s provider=%s device_id=%s model=%s", topic, provider, deviceID, config.Model)
				}

				// Audit log and sensor decode+write run concurrently: they target
				// different InfluxDB3 databases so there is no ordering requirement.
				auditPayload, auditDeviceID, auditNow := payload, deviceID, time.Now()
				go writeAuditLog(ctx, clients.AuditIoT, auditDeviceID, auditPayload, auditNow)

				parseMsg(ctx, clients.IoTSensors, provider, deviceID, devEUI, config, payload, currentReg)
			}
		}()
	}
	workerWG.Wait()
}
