package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	IoTSensors       *influxdb3.Client // iot_sensors — sensor_data
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
//   – battery voltage range for battery_level % calculation
//
// sensor_data (and audit_iot's raw) always holds exactly the value
// transmitted by the sensor, untouched — no calibration/unit-conversion math
// happens in this pipeline. That's a client-side/user-space concern for
// whatever consumes the SmartCampusMaua API.

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
	UC501   DeviceModel = "UC501"   // Milesight UC501/UC50x RS485/Modbus controller (Modbus + site-wired GPIO/analog input)
	UC511   DeviceModel = "UC511"   // Milesight UC511/UC512 irrigation valve + pipe-pressure controller (fixed-purpose, no RS485)
	VS370   DeviceModel = "VS370"   // Milesight VS370 single-zone occupancy/illuminance presence sensor
)

// DeviceConfig bundles model and battery range.
type DeviceConfig struct {
	Model             DeviceModel
	DeviceType        string // "LORAWAN" or "IP"
	SerialNumber      string
	MacAddress        string  // IP/WiFi devices — written as mac_address InfluxDB3 tag
	AssetID           string  // this device's own/"home" asset — see SensorConfig.AssetID for the per-sensor override
	BatteryVoltageMax float64 // V, full charge  (default 4.2 — LiPo/Li-Ion)
	BatteryVoltageMin float64 // V, cutoff       (default 3.3 — LoRa safe minimum)
	ProbeRangeM       float64 // DTL200: overrides bytes[3] when 0 (not configured via downlink)
}

// ─── Registry API client ───────────────────────────────────────────────────────
//
// devices/assets/sensors/modbus-channels are no longer this repo's own JSON
// files — the SmartCampusMaua `api` repo's Postgres-backed registry owns
// them now (the JSON files were a one-time bootstrap import, not the source
// of truth). These four fetches replace four file reads; every route read
// here is open (no auth token needed — this repo never writes).
var registryHTTPClient = &http.Client{Timeout: 10 * time.Second}

// fetchRegistryList performs one GET against the registry API and decodes a
// JSON array response into T. Shared by all four fetchers below — a network
// error, non-200 status, or unparseable body are all just "reload failed,"
// handled by the caller exactly like a failed file read used to be.
func fetchRegistryList[T any](url string) ([]T, error) {
	resp, err := registryHTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected status %s", url, resp.Status)
	}
	var out []T
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", url, err)
	}
	return out, nil
}

// ─── Device registry ─────────────────────────────────────────────────────────

// apiDevice is the raw shape of one row from GET /api/registry/v0.1/devices.
// Its own row identifier is "id" — unlike the old devices.json, where the
// entity's identifier was named "device_id" after itself. Don't confuse this
// with apiSensor.DeviceID/apiModbusChannel.DeviceID below, which are foreign
// keys *to* a device and correctly keep that descriptive name.
type apiDevice struct {
	ID                string      `json:"id"`
	DeviceModel       DeviceModel `json:"device_model"`
	DeviceType        string      `json:"device_type"` // "lorawan" or "ip"
	SerialNumber      string      `json:"serial_number"`
	DevEUI            string      `json:"dev_eui"`
	MacAddress        string      `json:"mac_address"`
	BatteryVoltageMax float64     `json:"battery_voltage_max"`
	BatteryVoltageMin float64     `json:"battery_voltage_min"`
	ProbeRangeM       float64     `json:"probe_range_m"`
	Allow             bool        `json:"allow"` // NOT NULL DEFAULT true on the API's side
	AssetID           string      `json:"asset_id"`
}

var reDevEUI = regexp.MustCompile(`(?i)^[0-9a-f]{16}$`)

// isUUID reports whether s is a syntactically valid UUID of any version.
// The registry API's own writes mint plain crypto.randomUUID() v4 ids (the
// original bootstrap-imported rows are v7, from this repo's old UUIDv7
// convention) — Postgres's own primary-key/FK constraints are the real
// integrity guarantee now, so this only checks basic shape, not version.
func isUUID(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}

// validateAPIDevice checks mandatory fields and format constraints for one
// device row. Returns an error for hard failures; logs warnings for soft issues.
func validateAPIDevice(d apiDevice) error {
	if d.ID == "" {
		return fmt.Errorf("id is required")
	}
	if !isUUID(d.ID) {
		return fmt.Errorf("device %s: id must be a valid UUID", d.ID)
	}
	if d.DeviceModel == "" {
		return fmt.Errorf("device %s: device_model is required", d.ID)
	}

	dt := strings.ToUpper(d.DeviceType)
	if dt != "LORAWAN" && dt != "IP" {
		return fmt.Errorf("device %s: device_type must be \"lorawan\" or \"ip\", got %q", d.ID, d.DeviceType)
	}

	if dt == "LORAWAN" {
		if d.DevEUI == "" {
			return fmt.Errorf("device %s: dev_eui is required for lorawan devices", d.ID)
		}
		if !reDevEUI.MatchString(d.DevEUI) {
			return fmt.Errorf("device %s: dev_eui %q must be 16 hex characters (EUI-64)", d.ID, d.DevEUI)
		}
	}

	if dt == "IP" {
		if d.MacAddress == "" {
			return fmt.Errorf("device %s: mac_address is required for ip devices", d.ID)
		}
		if _, err := net.ParseMAC(d.MacAddress); err != nil {
			return fmt.Errorf("device %s: mac_address %q is invalid: %w", d.ID, d.MacAddress, err)
		}
	}

	if d.SerialNumber == "" {
		log.Printf("[registry] warning: device %s (%s) has no serial_number", d.ID, d.DeviceModel)
	}

	return nil
}

// GetDevicesMap fetches the canonical device registry from the registry API.
// Returns two maps:
// - deviceID -> DeviceConfig
// - devEUI   -> deviceID
// All mandatory fields are validated; the whole fetch is rejected on any error.
func GetDevicesMap(apiBaseURL string) (map[string]DeviceConfig, map[string]string, error) {
	rows, err := fetchRegistryList[apiDevice](apiBaseURL + "/api/registry/v0.1/devices")
	if err != nil {
		return nil, nil, fmt.Errorf("fetch devices: %w", err)
	}
	log.Printf("[registry] devices=%d", len(rows))

	deviceMap := make(map[string]DeviceConfig, len(rows))
	devEUIToDeviceID := make(map[string]string, len(rows))

	for _, d := range rows {
		if !d.Allow {
			continue
		}

		if err := validateAPIDevice(d); err != nil {
			return nil, nil, fmt.Errorf("%w", err)
		}

		if _, exists := deviceMap[d.ID]; exists {
			return nil, nil, fmt.Errorf("duplicate device id in registry: %s", d.ID)
		}

		batteryMax := d.BatteryVoltageMax
		if batteryMax == 0 {
			batteryMax = 4.2
		}
		batteryMin := d.BatteryVoltageMin
		if batteryMin == 0 {
			batteryMin = 3.3
		}

		deviceMap[d.ID] = DeviceConfig{
			Model:             d.DeviceModel,
			DeviceType:        strings.ToUpper(d.DeviceType),
			SerialNumber:      d.SerialNumber,
			MacAddress:        strings.ToLower(d.MacAddress),
			AssetID:           d.AssetID,
			BatteryVoltageMax: batteryMax,
			BatteryVoltageMin: batteryMin,
			ProbeRangeM:       d.ProbeRangeM,
		}

		if d.DevEUI != "" {
			devEUINormalized := strings.ToLower(d.DevEUI)
			if mapped, exists := devEUIToDeviceID[devEUINormalized]; exists && mapped != d.ID {
				return nil, nil, fmt.Errorf("duplicate dev_eui mapping for %s (%s and %s)", devEUINormalized, mapped, d.ID)
			}
			devEUIToDeviceID[devEUINormalized] = d.ID
		}
	}

	return deviceMap, devEUIToDeviceID, nil
}

// ─── Asset registry ───────────────────────────────────────────────────────────

// apiAsset is the raw shape of one row from GET /api/registry/v0.1/assets.
// asset_name/asset_slug/asset_coords/parent_asset_id are intentionally not
// modeled here — this repo only ever needed an asset's id (for the asset_id
// tag), never its metadata. Fetched purely to validate that devices/sensors
// still reference an asset that actually exists — see validateAssetReferences.
type apiAsset struct {
	ID string `json:"id"`
}

// GetAssetIDSet fetches every known asset id from the registry API, for
// validateAssetReferences to check device/sensor asset_id values against.
func GetAssetIDSet(apiBaseURL string) (map[string]bool, error) {
	rows, err := fetchRegistryList[apiAsset](apiBaseURL + "/api/registry/v0.1/assets")
	if err != nil {
		return nil, fmt.Errorf("fetch assets: %w", err)
	}
	log.Printf("[registry] assets=%d", len(rows))

	out := make(map[string]bool, len(rows))
	for _, a := range rows {
		out[a.ID] = true
	}
	return out, nil
}

// validateAssetReferences logs a warning (never fails a reload) for every
// device/sensor whose asset_id doesn't match a currently-known asset — same
// lenient, soft-FK philosophy this file has always used for cross-entity
// references that might just be a timing gap, not a real error.
func validateAssetReferences(deviceMap map[string]DeviceConfig, sensorMap map[string]SensorConfig, validAssetIDs map[string]bool) {
	for deviceID, cfg := range deviceMap {
		if cfg.AssetID != "" && !validAssetIDs[cfg.AssetID] {
			log.Printf("[registry] warning: device %s references asset_id %s which does not exist", deviceID, cfg.AssetID)
		}
	}
	for sensorID, s := range sensorMap {
		if s.AssetID != "" && !validAssetIDs[s.AssetID] {
			log.Printf("[registry] warning: sensor %s references asset_id %s which does not exist", sensorID, s.AssetID)
		}
	}
}

// ─── Sensor registry ──────────────────────────────────────────────────────────

// apiSensor is the raw shape of one row from GET /api/registry/v0.1/sensors.
// Its own row identifier is "id" (see apiDevice's doc comment on this naming
// trap); DeviceID here is a genuine foreign key to a device, unchanged.
type apiSensor struct {
	ID          string `json:"id"`
	SensorType  string `json:"sensor_type"`
	DeviceID    string `json:"device_id"`
	DeviceIndex int    `json:"device_index"`
	SensorName  string `json:"sensor_name"`
	// AssetID is an override, independent of the sensor's device's own
	// asset_id — set only for a sensor on a bus-listening device (e.g. a
	// UC300) that's physically relaying readings for a different asset than
	// wherever the device itself sits. Effective asset for a sensor is
	// sensor.AssetID if set, else the device's own AssetID — see the
	// resolution step in parseDeviceModel.
	AssetID string `json:"asset_id"`
}

// SensorConfig is the resolved projection of one apiSensor, keyed by
// sensor_id in the map GetSensorsMap returns.
type SensorConfig struct {
	SensorType  string
	DeviceID    string
	DeviceIndex int
	SensorName  string
	AssetID     string // override — see apiSensor.AssetID
}

// validateAPISensor checks mandatory fields and format constraints for one
// sensor row. validTypes is record.AllSensorTypes(), computed once by the
// caller rather than per-entry. Returns an error for hard failures.
func validateAPISensor(s apiSensor, validTypes map[string]bool) error {
	if s.ID == "" {
		return fmt.Errorf("id is required")
	}
	if !isUUID(s.ID) {
		return fmt.Errorf("sensor %s: id must be a valid UUID", s.ID)
	}
	if s.SensorType == "" {
		return fmt.Errorf("sensor %s: sensor_type is required", s.ID)
	}
	if !validTypes[s.SensorType] {
		return fmt.Errorf("sensor %s: sensor_type %q is not a recognized sensor_type (see record.SensorTypes) — add it there first if it's genuinely new", s.ID, s.SensorType)
	}
	if s.DeviceID == "" {
		return fmt.Errorf("sensor %s: device_id is required", s.ID)
	}
	if !isUUID(s.DeviceID) {
		return fmt.Errorf("sensor %s: device_id must be a valid UUID", s.ID)
	}
	if s.DeviceIndex < 0 {
		return fmt.Errorf("sensor %s: device_index must not be negative", s.ID)
	}
	return nil
}

// GetSensorsMap fetches the canonical sensor registry from the registry API.
// Returns sensorID -> SensorConfig. All mandatory fields are validated; the
// whole fetch is rejected on any error. Cross-entity references (device_id
// existing in the device registry) are deliberately NOT checked here — same
// decoupling as before: a row pointing at a device_id that doesn't (yet)
// exist just never resolves to anything at decode time, rather than failing
// the whole fetch. Not unique on (device_id, sensor_type) alone — two
// sensors of the same type can legitimately share a device (e.g. three
// soil-moisture probes at different depths); device_index disambiguates
// them, and (device_id, sensor_type, device_index) together must be unique.
func GetSensorsMap(apiBaseURL string) (map[string]SensorConfig, error) {
	rows, err := fetchRegistryList[apiSensor](apiBaseURL + "/api/registry/v0.1/sensors")
	if err != nil {
		return nil, fmt.Errorf("fetch sensors: %w", err)
	}
	log.Printf("[registry] sensors=%d", len(rows))

	validTypes := record.AllSensorTypes()
	sensorMap := make(map[string]SensorConfig, len(rows))
	seenTuple := make(map[string]string, len(rows)) // (device_id,sensor_type,device_index) -> sensor_id

	for _, s := range rows {
		if err := validateAPISensor(s, validTypes); err != nil {
			return nil, fmt.Errorf("%w", err)
		}
		if _, exists := sensorMap[s.ID]; exists {
			return nil, fmt.Errorf("duplicate sensor id in registry: %s", s.ID)
		}
		tupleKey := sensorIndexKey(s.DeviceID, s.SensorType, s.DeviceIndex)
		if existing, exists := seenTuple[tupleKey]; exists {
			return nil, fmt.Errorf("device_id/sensor_type/device_index already assigned to sensor %s: %s", existing, tupleKey)
		}
		seenTuple[tupleKey] = s.ID
		sensorMap[s.ID] = SensorConfig{
			SensorType: s.SensorType, DeviceID: s.DeviceID, DeviceIndex: s.DeviceIndex,
			SensorName: s.SensorName, AssetID: s.AssetID,
		}
	}

	return sensorMap, nil
}

// ─── Modbus channel registry (UC100/UC300 RS485 routing) ─────────────────────

// apiModbusChannel is the raw shape of one row from GET
// /api/registry/v0.1/modbus-channels — already flattened/joined server-side
// (the underlying device->bus->channel table structure isn't exposed here).
// There is currently no equivalent endpoint for UC300's fixed-hardware IO
// channels (GPIO/PT100/ADC) — see DeviceChannels.IO's doc comment.
type apiModbusChannel struct {
	ID            string `json:"id"`
	DeviceID      string `json:"device_id"`
	BusType       string `json:"bus_type"`
	SensorID      string `json:"sensor_id"`
	ModbusChannel int    `json:"modbus_channel"` // 1-32; protocol never uses 0
	ModbusSlaveID int    `json:"modbus_slave_id"`
}

// DeviceChannels holds one UC-series device's channel->sensor_id routing.
type DeviceChannels struct {
	Modbus map[int]string // modbus_channel -> sensor_id
	// IO would be io_channel -> sensor_id (UC300's fixed-hardware GPIO/PT100/
	// ADC pins), but the registry API has no io-channels endpoint yet —
	// always empty until one exists. No currently-deployed device relies on
	// this (every real device_channels.json entry to date has been Modbus
	// only), so this is a known gap, not a regression in practice today.
	IO map[int]string
}

// GetDeviceChannelsMap fetches the Modbus channel routing registry from the
// registry API. Returns deviceID -> DeviceChannels. Routing only — never
// carries sensor_type. device_id/sensor_id existence in the device/sensor
// registries is deliberately not checked here, same decoupling as
// GetAssetIDSet/GetSensorsMap — a row referencing something that doesn't
// exist yet just never resolves to a tag at decode time. A channel/slave
// combination reused across two rows on the same device IS a hard error
// (mirrored by the API's own unique constraint), since that's a real,
// structural misconfiguration, not a timing gap.
func GetDeviceChannelsMap(apiBaseURL string) (map[string]DeviceChannels, error) {
	rows, err := fetchRegistryList[apiModbusChannel](apiBaseURL + "/api/registry/v0.1/modbus-channels")
	if err != nil {
		return nil, fmt.Errorf("fetch modbus-channels: %w", err)
	}
	log.Printf("[registry] modbus_channels=%d", len(rows))

	out := make(map[string]DeviceChannels)
	seenSensorID := make(map[string]bool, len(rows))

	for _, e := range rows {
		if e.DeviceID == "" || e.SensorID == "" || e.ModbusChannel == 0 || e.ModbusSlaveID == 0 {
			return nil, fmt.Errorf("modbus channel %s: device_id/sensor_id/modbus_channel/modbus_slave_id must all be set", e.ID)
		}
		if seenSensorID[e.SensorID] {
			return nil, fmt.Errorf("duplicate sensor_id in modbus channel registry: %s", e.SensorID)
		}
		seenSensorID[e.SensorID] = true

		dc, ok := out[e.DeviceID]
		if !ok {
			dc = DeviceChannels{Modbus: make(map[int]string), IO: make(map[int]string)}
		}
		if existing, exists := dc.Modbus[e.ModbusChannel]; exists {
			return nil, fmt.Errorf("device %s: modbus_channel %d already assigned to sensor %s", e.DeviceID, e.ModbusChannel, existing)
		}
		dc.Modbus[e.ModbusChannel] = e.SensorID
		out[e.DeviceID] = dc
	}

	return out, nil
}

// sensorIndexKey joins deviceID+sensorType+deviceIndex into the reverse-index
// key used by buildSensorIndex/resolveSensorID. Not exported; the separator
// can't appear in a UUIDv7 or a record.ST value, so a plain join is safe.
// deviceIndex is 0 for every single-instance sensor_type — same as the zero
// value of record.SensorDataRecord.DeviceIndex, so callers never need to
// special-case it.
func sensorIndexKey(deviceID, sensorType string, deviceIndex int) string {
	return deviceID + "\x00" + sensorType + "\x00" + strconv.Itoa(deviceIndex)
}

// buildSensorIndex derives a (device_id, sensor_type, device_index) ->
// sensor_id lookup from sensors.json, used to stamp a stable sensor_id onto
// every decoded record without decoders needing to know about the sensor
// registry at all.
//
// sensors.json is deliberately NOT unique on (device_id, sensor_type) alone
// — e.g. three soil-moisture probes on one device share a sensor_type,
// disambiguated by device_index (GetSensorsMap enforces uniqueness on the
// full (device_id, sensor_type, device_index) triple). A tuple that's STILL
// ambiguous even including device_index (shouldn't happen given that
// enforcement, but this index doesn't re-trust it) is left out here (logged,
// not an error) rather than resolved arbitrarily.
func buildSensorIndex(sensorMap map[string]SensorConfig) map[string]string {
	counts := make(map[string]int, len(sensorMap))
	sensorIDByKey := make(map[string]string, len(sensorMap))
	for sensorID, s := range sensorMap {
		key := sensorIndexKey(s.DeviceID, s.SensorType, s.DeviceIndex)
		counts[key]++
		sensorIDByKey[key] = sensorID
	}
	index := make(map[string]string, len(sensorIDByKey))
	for key, sensorID := range sensorIDByKey {
		if counts[key] > 1 {
			log.Printf("[registry] warning: device_id/sensor_type/device_index tuple is ambiguous in sensors.json (%d sensors share it) — skipping from sensor_id index: %s", counts[key], key)
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

// buildSensorPoints converts decoded records into valid InfluxDB3 points,
// dropping (and logging) any record that fails validation. Pure — no I/O.
// The actual write happens later, fanned into a single batchWriter (see its
// doc comment below) instead of one WritePoints call per message here.
func buildSensorPoints(records []record.SensorDataRecord) []*influxdb3.Point {
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
		if r.AssetID != "" {
			p = p.SetTag("asset_id", r.AssetID)
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
	return points
}

// influxWriter is the subset of *influxdb3.Client this file depends on,
// narrowed to an interface purely so tests can substitute a fake instead of
// making a real HTTP call. *influxdb3.Client satisfies this as-is — nothing
// about production behavior changes.
type influxWriter interface {
	WritePoints(ctx context.Context, points []*influxdb3.Point, options ...influxdb3.WriteOption) error
}

// writeWithRetry attempts to write points up to maxAttempts times with
// exponential back-off. Unrecoverable 4xx errors (except 429) are returned
// immediately without retrying.
func writeWithRetry(ctx context.Context, iot influxWriter, points []*influxdb3.Point, maxAttempts int) error {
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

// ─────────────────────────────────────────────────────────────────────────────
// Batch writer — fans in points from many concurrent decode workers into one
// InfluxDB3 client
// ─────────────────────────────────────────────────────────────────────────────
//
// Previously, every worker goroutine called WritePoints directly, once per
// MQTT message — with WRITE_WORKERS (default 2×GOMAXPROCS, 8+ on a typical
// host) all doing this independently and concurrently, every device uplink
// became its own tiny, uncoordinated write request. Direct inspection of the
// on-disk Parquet files showed this fragmenting into 2-4 separate files per
// 10-minute window instead of one — confirmed by disjoint sensor sets across
// those files (the signature of multiple independent write streams landing
// in the same window, not one writer flushing once). That multiplies the
// effective file count InfluxDB3 has to scan for any time-range query,
// eventually exceeding its file-scan cap even for ranges well inside the
// nominal safe window.
//
// batchWriter fixes this by making exactly one goroutine responsible for
// ever calling WritePoints against a given InfluxDB3 client. Decode workers
// keep running fully in parallel — they just hand their finished points to
// this writer over a channel instead of writing them directly — and the
// writer accumulates into a buffer, flushing on a timer or once the buffer
// hits a size threshold, whichever comes first. One writer, one flush at a
// time, one InfluxDB3 write stream per client — no more concurrent
// uncoordinated writers to fragment across.
type batchWriter struct {
	client   influxWriter
	name     string // for logging, e.g. "sensor_data" / "raw"
	batches  chan []*influxdb3.Point
	interval time.Duration
	maxBatch int
	wg       sync.WaitGroup
}

// writeBatchChanCapacity is how many message-batches (not points — each
// Enqueue call pushes one message's worth of points as a single slice) the
// writer's input channel can hold before Enqueue starts blocking the calling
// decode worker. Generously sized so a slow flush doesn't stall decoding;
// not exposed as an env var since WRITE_FLUSH_INTERVAL_SEC/WRITE_BATCH_SIZE
// are the actual tuning knobs — this just needs to be "big enough."
const writeBatchChanCapacity = 4096

// newBatchWriter starts the writer's background flush loop and returns
// immediately; call Close to flush whatever's buffered and stop it.
func newBatchWriter(client influxWriter, name string, interval time.Duration, maxBatch int) *batchWriter {
	bw := &batchWriter{
		client:   client,
		name:     name,
		batches:  make(chan []*influxdb3.Point, writeBatchChanCapacity),
		interval: interval,
		maxBatch: maxBatch,
	}
	bw.wg.Add(1)
	go bw.run()
	return bw
}

// Enqueue hands one message's worth of points to the writer. Non-blocking in
// practice (buffered channel); only blocks the caller if the writer has
// fallen far enough behind to fill writeBatchChanCapacity, which is the
// intended backpressure signal if flush interval/batch size ever need retuning.
func (bw *batchWriter) Enqueue(points []*influxdb3.Point) {
	if len(points) == 0 {
		return
	}
	bw.batches <- points
}

// Close does not itself stop new work from arriving — callers must stop
// calling Enqueue first (e.g. by draining/closing whatever feeds them).
// Close closes the input channel and blocks until run has flushed
// everything already buffered and exited — the graceful-shutdown guarantee
// that no enqueued points are silently lost on a clean shutdown.
func (bw *batchWriter) Close() {
	close(bw.batches)
	bw.wg.Wait()
}

func (bw *batchWriter) run() {
	defer bw.wg.Done()
	ticker := time.NewTicker(bw.interval)
	defer ticker.Stop()
	ctx := context.Background()
	buf := make([]*influxdb3.Point, 0, bw.maxBatch)

	flush := func() {
		if len(buf) == 0 {
			return
		}
		bw.writeBatch(ctx, buf)
		buf = make([]*influxdb3.Point, 0, bw.maxBatch)
	}

	for {
		select {
		case batch, ok := <-bw.batches:
			if !ok {
				flush() // final flush on shutdown — see Close
				return
			}
			buf = append(buf, batch...)
			if len(buf) >= bw.maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// writeBatch writes one consolidated batch, retrying the batch as a whole
// first; if that still fails, falls back to per-point writes so one bad
// point doesn't drop the entire flush interval's data — the same resilience
// shape this file has always used, just applied to the larger consolidated
// batch instead of one message's points.
func (bw *batchWriter) writeBatch(ctx context.Context, points []*influxdb3.Point) {
	const maxAttempts = 3
	batchErr := writeWithRetry(ctx, bw.client, points, maxAttempts)
	if batchErr == nil {
		log.Printf("[influxdb3] wrote %d points to %s", len(points), bw.name)
		return
	}
	// Batch failed — fall back to per-point writes so one bad point does not
	// silently drop the whole flush interval's data.
	logWarn("%s: batch of %d points failed (%v) – retrying individually", bw.name, len(points), batchErr)

	var dropped int
	for i, pt := range points {
		if err := writeWithRetry(ctx, bw.client, []*influxdb3.Point{pt}, maxAttempts); err != nil {
			// Include the tag values so the bad point is identifiable in logs.
			devID, _ := pt.GetTag("device_id")
			sensorType, _ := pt.GetTag("sensor_type")
			logError("%s: point %d/%d dropped permanently: device_id=%s sensor_type=%s error=%v",
				bw.name, i+1, len(points), devID, sensorType, err)
			dropped++
		}
	}
	if dropped > 0 {
		logError("%s: %d/%d points dropped from this flush", bw.name, dropped, len(points))
	}
}

// buildAuditPoint builds (but does not write) one raw-payload audit point.
//
// Schema (measurement = "raw"):
//
//	Tags:  device_id                   — identifies the physical device
//	       asset_id                    — whichever asset the device was assigned to at
//	                                      this instant (see the registry API); omitted when unassigned
//	       event_type = "payload_ingest" — fixed; low-cardinality; extensible
//	Field: raw_data  (string ≤512 chars) — original undecoded message
//	Time:  ingestion timestamp (time.Now() at message receipt)
//
// asset_id here is deliberately just a snapshot of "current" at each instant,
// same as every other tag on this row — raw is already a continuous,
// timestamped record of every message, so the full assignment history is
// already reconstructable from it without any separate change-tracking.
//
// The audit database carries a short retention policy set on the InfluxDB3 server
// (e.g., 7–30 days) independently of the sensor_data database.
func buildAuditPoint(deviceID, assetID, rawData string, ts time.Time) *influxdb3.Point {
	const maxLen = 512
	if len(rawData) > maxLen {
		rawData = rawData[:maxLen] + "…"
	}
	p := influxdb3.NewPointWithMeasurement("raw").
		SetTag("device_id", deviceID).
		SetTag("event_type", "payload_ingest").
		SetStringField("raw_data", rawData).
		SetTimestamp(ts)
	if assetID != "" {
		p = p.SetTag("asset_id", assetID)
	}
	return p
}

// writeAuditLog hands one raw-payload audit point to auditWriter — see
// batchWriter's doc comment for why this is no longer a direct WritePoints
// call per message.
func writeAuditLog(auditWriter *batchWriter, deviceID, assetID, rawData string, ts time.Time) {
	auditWriter.Enqueue([]*influxdb3.Point{buildAuditPoint(deviceID, assetID, rawData, ts)})
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

// effectiveAssetID resolves which asset a record should be tagged with: the
// sensor's own asset_id override if it has one, else the device's own home
// asset (deviceAssetID). This is the fix for a bus-listening device (e.g. a
// UC300) that's physically mounted on one asset while relaying sensor
// readings that actually belong to a *different* asset reachable on the
// same bus — see apiSensor.AssetID's doc comment for the full rationale.
// Computing this from the device alone (ignoring the sensor's own override)
// would silently misattribute every such sensor's data to the wrong asset.
func effectiveAssetID(deviceAssetID string, sensor SensorConfig) string {
	if sensor.AssetID != "" {
		return sensor.AssetID
	}
	return deviceAssetID
}

// parseDeviceModel decodes rawPayload and writes sensor_data points to InfluxDB3.
//
// provider == "custom": rawPayload is raw JSON; timestamp comes from the message.
// LNS providers:        rawPayload is base64-encoded binary; timestamp from LNS frame.
func parseDeviceModel(sensorWriter *batchWriter,
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
		r.AssetID = config.AssetID
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
			if sensorID, ok := reg.SensorIndex[sensorIndexKey(deviceID, r.SensorType, r.DeviceIndex)]; ok {
				r.SensorID = sensorID
			}
		}
		// Only applies once SensorID is actually resolved; an unresolved
		// reading keeps the device-level default already set on r.AssetID above.
		if r.SensorID != "" {
			r.AssetID = effectiveAssetID(r.AssetID, reg.SensorMap[r.SensorID])
		}
		resolved = append(resolved, r)
	}
	records = resolved
	if len(records) == 0 {
		log.Printf("[parse] all records dropped after sensor resolution for %s (%s)", deviceID, deviceModel)
		return
	}
	points := buildSensorPoints(records)
	if len(points) == 0 {
		log.Printf("[parse] no valid points after building for %s (%s)", deviceID, deviceModel)
		return
	}
	sensorWriter.Enqueue(points)
	if devEUI != "" {
		log.Printf("[influxdb3] queued %d points: device_model=%s device_id=%s dev_eui=%s",
			len(points), strings.ToLower(string(deviceModel)), deviceID, devEUI)
	} else {
		log.Printf("[influxdb3] queued %d points: device_model=%s device_id=%s",
			len(points), strings.ToLower(string(deviceModel)), deviceID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Message entry point – single provider switch, no redundant checks
// ─────────────────────────────────────────────────────────────────────────────

// parseMsg routes an incoming MQTT message to the correct decoder.
func parseMsg(sensorWriter *batchWriter, provider, deviceID, devEUI string, config DeviceConfig, message string, reg registrySnapshot) {
	if message == "" {
		return
	}
	switch provider {

	case "custom":
		// Direct MQTT device: raw JSON, no LNS frame wrapping.
		parseDeviceModel(sensorWriter, deviceID, "", config, "custom", message, 0, time.Time{}, reg)

	case "chirpstackv4":
		frame, err := chirpstack.Parse(message)
		if err != nil {
			log.Printf("[parseMsg/chirpstackv4] %v (deviceID=%s)", err, deviceID)
			return
		}
		parseDeviceModel(sensorWriter, deviceID, devEUI, config, "chirpstackv4", frame.Data, frame.Port, frame.Timestamp, reg)

	case "everynet":
		frame, err := everynet.Parse(message)
		if err != nil {
			log.Printf("[parseMsg/everynet] %v (deviceID=%s)", err, deviceID)
			return
		}
		parseDeviceModel(sensorWriter, deviceID, devEUI, config, "everynet", frame.Data, frame.Port, frame.Timestamp, reg)

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

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found; using environment variables")
	}

	// INFRA_CONFIG_FILE holds InfluxDB3 + MQTT connection details (host,
	// token, broker, credentials) — see infra_config.go's doc comment for
	// why this is a JSON file rather than env vars.
	INFRA_CONFIG_FILE := os.Getenv("INFRA_CONFIG_FILE")
	if INFRA_CONFIG_FILE == "" {
		INFRA_CONFIG_FILE = "infra.json"
	}
	infraCfg, err := loadInfraConfig(INFRA_CONFIG_FILE)
	if err != nil {
		log.Fatalf("[infra] %v", err)
	}
	INFLUXDB_HOST := infraCfg.InfluxDB.Host
	INFLUXDB_TOKEN := infraCfg.InfluxDB.Token

	// REGISTRY_API_URL points at the SmartCampusMaua `api` repo's registry
	// endpoints (devices/assets/sensors/modbus-channels) — the source of
	// truth for all four now, replacing this repo's own JSON files. Reads
	// are open on that API; no token needed.
	REGISTRY_API_URL := os.Getenv("REGISTRY_API_URL")
	if REGISTRY_API_URL == "" {
		REGISTRY_API_URL = "http://localhost:3333"
	}
	DEVICE_REGISTRY_REFRESH_SEC := 30
	if raw := os.Getenv("DEVICE_REGISTRY_REFRESH_SEC"); raw != "" {
		if v, convErr := strconv.Atoi(raw); convErr == nil && v > 0 {
			DEVICE_REGISTRY_REFRESH_SEC = v
		}
	}
	// WRITE_FLUSH_INTERVAL_SEC/WRITE_BATCH_SIZE tune the batchWriter fan-in
	// (see its doc comment) — how long a batch of points sits buffered
	// before being flushed, and how many points force an early flush.
	WRITE_FLUSH_INTERVAL_SEC := 5
	if raw := os.Getenv("WRITE_FLUSH_INTERVAL_SEC"); raw != "" {
		if v, convErr := strconv.Atoi(raw); convErr == nil && v > 0 {
			WRITE_FLUSH_INTERVAL_SEC = v
		}
	}
	WRITE_BATCH_SIZE := 5000
	if raw := os.Getenv("WRITE_BATCH_SIZE"); raw != "" {
		if v, convErr := strconv.Atoi(raw); convErr == nil && v > 0 {
			WRITE_BATCH_SIZE = v
		}
	}

	// Initialize all four InfluxDB3 clients on the same host+token.
	// Database names are hardcoded; each client writes to a different database.
	var clients DBClients

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

	// Exactly one goroutine each ever calls WritePoints against iot_sensors /
	// audit_iot from here on — see batchWriter's doc comment for why (every
	// decode worker calling WritePoints directly, once per MQTT message,
	// fragmented InfluxDB3's Parquet files: confirmed via direct inspection,
	// 2-4 separate files per 10-minute window instead of one, eventually
	// exceeding InfluxDB3's file-scan cap on ordinary time-range queries).
	flushInterval := time.Duration(WRITE_FLUSH_INTERVAL_SEC) * time.Second
	sensorWriter := newBatchWriter(clients.IoTSensors, "sensor_data", flushInterval, WRITE_BATCH_SIZE)
	auditWriter := newBatchWriter(clients.AuditIoT, "raw", flushInterval, WRITE_BATCH_SIZE)
	log.Printf("[main] batch writers started: flushInterval=%s batchSize=%d", flushInterval, WRITE_BATCH_SIZE)

	deviceMap, devEUIToDeviceID, err := GetDevicesMap(REGISTRY_API_URL)
	if err != nil {
		panic(fmt.Sprintf("failed to load device registry (%s): %v", REGISTRY_API_URL, err))
	}
	log.Printf("Device registry: api=%s deviceIDs=%d devEUI mappings=%d", REGISTRY_API_URL, len(deviceMap), len(devEUIToDeviceID))

	// Asset registry is supplementary metadata (no decoder depends on it), so
	// a bad/unreachable registry API logs a warning and starts with no asset
	// validation rather than crashing the service like a bad device fetch would.
	validAssetIDs, assetErr := GetAssetIDSet(REGISTRY_API_URL)
	if assetErr != nil {
		log.Printf("[registry] warning: failed to load asset registry (%s): %v", REGISTRY_API_URL, assetErr)
		validAssetIDs = map[string]bool{}
	}
	log.Printf("Asset registry: api=%s assets=%d", REGISTRY_API_URL, len(validAssetIDs))

	// Sensor and device-channel registries are supplementary metadata (no
	// decoder depends on them to produce a value — only to tag it with a
	// stable sensor_id), so a bad/unreachable fetch logs a warning and starts
	// with empty maps rather than crashing the service, mirroring assets.
	sensorMap, sensorErr := GetSensorsMap(REGISTRY_API_URL)
	if sensorErr != nil {
		log.Printf("[registry] warning: failed to load sensor registry (%s): %v", REGISTRY_API_URL, sensorErr)
		sensorMap = map[string]SensorConfig{}
	}
	validateAssetReferences(deviceMap, sensorMap, validAssetIDs)
	deviceChannels, channelErr := GetDeviceChannelsMap(REGISTRY_API_URL)
	if channelErr != nil {
		log.Printf("[registry] warning: failed to load device channel registry (%s): %v", REGISTRY_API_URL, channelErr)
		deviceChannels = map[string]DeviceChannels{}
	}
	sensorIndex := buildSensorIndex(sensorMap)
	log.Printf("Sensor registry: api=%s sensors=%d index=%d", REGISTRY_API_URL, len(sensorMap), len(sensorIndex))
	log.Printf("Device channel registry: api=%s devicesWithChannels=%d", REGISTRY_API_URL, len(deviceChannels))

	var registryMu sync.RWMutex
	refreshTicker := time.NewTicker(time.Duration(DEVICE_REGISTRY_REFRESH_SEC) * time.Second)
	defer refreshTicker.Stop()
	go func() {
		for range refreshTicker.C {
			nextDeviceMap, nextDevEUIToDeviceID, reloadErr := GetDevicesMap(REGISTRY_API_URL)
			if reloadErr != nil {
				log.Printf("[registry] reload failed: %v", reloadErr)
				continue
			}
			// Keep the last known-good asset id set on a transient asset
			// fetch failure, rather than wiping validation just because the
			// device fetch happened to succeed.
			if nextValidAssetIDs, assetReloadErr := GetAssetIDSet(REGISTRY_API_URL); assetReloadErr != nil {
				log.Printf("[registry] asset reload failed, keeping previous asset id set: %v", assetReloadErr)
			} else {
				validAssetIDs = nextValidAssetIDs
			}

			// Same last-known-good behavior for sensors/modbus-channels.
			// Computed into local vars first so deviceChannels/sensorMap/sensorIndex
			// (all read by workers) flip together atomically under registryMu.
			nextSensorMap := sensorMap
			if m, sensorReloadErr := GetSensorsMap(REGISTRY_API_URL); sensorReloadErr != nil {
				log.Printf("[registry] sensor registry reload failed, keeping previous sensor index: %v", sensorReloadErr)
			} else {
				nextSensorMap = m
			}
			validateAssetReferences(nextDeviceMap, nextSensorMap, validAssetIDs)

			nextDeviceChannels := deviceChannels
			if m, channelReloadErr := GetDeviceChannelsMap(REGISTRY_API_URL); channelReloadErr != nil {
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
			log.Printf("[registry] reloaded: api=%s deviceIDs=%d devEUI mappings=%d assets=%d sensors=%d sensorIndex=%d devicesWithChannels=%d",
				REGISTRY_API_URL, len(nextDeviceMap), len(nextDevEUIToDeviceID), len(validAssetIDs), len(nextSensorMap), len(nextSensorIndex), len(nextDeviceChannels))
		}
	}()

	// connLost signals the shutdown goroutine below instead of calling
	// os.Exit directly (as this used to) — an immediate exit would now skip
	// draining the decode workers and flushing the batch writers, abandoning
	// up to a full flush interval's worth of buffered points. Buffered size
	// 1 + non-blocking send: only the first loss matters, Paho may report
	// more before the process actually exits.
	connLost := make(chan error, 1)

	id := uuid.New().String()
	mqttOpts := MQTT.NewClientOptions().
		AddBroker(infraCfg.MQTT.Broker).
		SetClientID("parse-lns-sub-" + id).
		SetUsername(infraCfg.MQTT.Username).
		SetPassword(infraCfg.MQTT.Password).
		SetConnectionLostHandler(func(_ MQTT.Client, err error) {
			log.Printf("MQTT connection lost: %v", err)
			select {
			case connLost <- err:
			default:
			}
		})

	incoming := make(chan [2]string, 256)
	mqttOpts.SetDefaultPublishHandler(func(_ MQTT.Client, msg MQTT.Message) {
		incoming <- [2]string{msg.Topic(), string(msg.Payload())}
	})

	mqttClient := MQTT.NewClient(mqttOpts)
	if tok := mqttClient.Connect(); tok.Wait() && tok.Error() != nil {
		panic(tok.Error())
	}
	log.Printf("MQTT: connected to %s", infraCfg.MQTT.Broker)

	// Subscribe to  device/DEVICE_ID/telemetry  or the broader wildcard below.
	subTopic := "device/+/telemetry"
	if tok := mqttClient.Subscribe(subTopic, 0, nil); tok.Wait() && tok.Error() != nil {
		log.Fatal(tok.Error())
	}
	log.Printf("MQTT: subscribed to %s", subTopic)

	// Graceful shutdown: on SIGINT/SIGTERM (or an unexpected MQTT connection
	// loss, see connLost above), stop new MQTT messages from arriving, let
	// already-queued ones drain through the decode workers, then flush
	// whatever the batch writers still have buffered — so neither a clean
	// shutdown (e.g. `docker stop`) nor a dropped connection ever silently
	// discards buffered points the way an untracked in-flight write would.
	//
	// shutdownDueToConnLoss is written here before close(incoming) and only
	// read after workerWG.Wait() returns below — safe without atomics, since
	// closing a channel is itself a happens-before synchronization point in
	// Go's memory model, and that close-then-drain chain is what makes
	// workerWG.Wait() return in the first place.
	var shutdownDueToConnLoss bool
	shutdownSig := make(chan os.Signal, 1)
	signal.Notify(shutdownSig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-shutdownSig:
			log.Printf("[main] received %s, shutting down: disconnecting MQTT, draining workers...", sig)
		case err := <-connLost:
			shutdownDueToConnLoss = true
			log.Printf("[main] MQTT connection lost (%v) — shutting down so the process can be restarted...", err)
		}
		mqttClient.Disconnect(1000)
		close(incoming)
	}()

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

				// Audit log and sensor decode+write both just enqueue onto their
				// own batchWriter now (see its doc comment) — fast, non-blocking
				// channel sends, no longer worth a dedicated goroutine each like
				// when writeAuditLog did a direct blocking WritePoints call. That
				// also avoids a real shutdown race: an untracked `go` here could
				// still be enqueueing after workerWG.Wait() returns, racing
				// against auditWriter.Close().
				auditPayload, auditDeviceID, auditAssetID, auditNow := payload, deviceID, config.AssetID, time.Now()
				writeAuditLog(auditWriter, auditDeviceID, auditAssetID, auditPayload, auditNow)

				parseMsg(sensorWriter, provider, deviceID, devEUI, config, payload, currentReg)
			}
		}()
	}
	workerWG.Wait()

	// All decode workers have exited (incoming is closed and drained), so
	// nothing can still be calling Enqueue — safe to close the batch writers
	// now. Close blocks until each has flushed whatever it still had buffered.
	log.Printf("[main] all workers drained, flushing batch writers...")
	sensorWriter.Close()
	auditWriter.Close()
	if shutdownDueToConnLoss {
		log.Printf("[main] shutdown complete (connection loss) — exiting non-zero so the process gets restarted")
		os.Exit(1)
	}
	log.Printf("[main] shutdown complete")
}
