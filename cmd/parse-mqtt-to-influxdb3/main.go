package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"regexp"
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
	"github.com/OpenDataTelemetry/device-gateway-mqtt/internal/record"
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
	DTL200  DeviceModel = "DTL200"  // Khomp DTL200 series
	EM300   DeviceModel = "EM300"   // Milesight EM300 series
	EM500   DeviceModel = "EM500"   // Milesight EM500 series
	KS3000  DeviceModel = "KS3000"  // Kron KS3000 series
	NIT21LI DeviceModel = "NIT21LI" // Khomp NIT21LI model
	WS101   DeviceModel = "WS101"   // Milesight smart button model
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
type DeviceEntry struct {
	Allow             *bool               `json:"allow,omitempty"`
	DeviceID          string              `json:"device_id,omitempty"`
	DeviceType        string              `json:"device_type,omitempty"` // "lorawan" or "ip"
	SerialNumber      *string             `json:"serial_number"`         // required; warn if empty
	AssetID           string              `json:"asset_id,omitempty"`
	AssetCoords       *AssetCoords        `json:"asset_coords,omitempty"`
	DevEUI            string              `json:"dev_eui,omitempty"`     // mandatory for lorawan
	MacAddress        string              `json:"mac_address,omitempty"` // mandatory for ip
	DeviceModel       DeviceModel         `json:"device_model,omitempty"`
	BatteryVoltageMax float64             `json:"battery_voltage_max,omitempty"`
	BatteryVoltageMin float64             `json:"battery_voltage_min,omitempty"`
	ProbeRangeM       float64             `json:"probe_range_m,omitempty"`
	Calibrations      []DeviceCalibration `json:"calibrations,omitempty"`
}

// AssetCoords stores optional device geolocation metadata.
type AssetCoords struct {
	Lng float64 `json:"lng,omitempty"`
	Lat float64 `json:"lat,omitempty"`
	Alt float64 `json:"alt,omitempty"`
}

type DeviceRegistryFile struct {
	Version string        `json:"version,omitempty"`
	Devices []DeviceEntry `json:"devices"`
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

	if e.AssetID != "" && !isUUIDv7(e.AssetID) {
		return fmt.Errorf("device %s: asset_id %q must be a valid UUIDv7", e.DeviceID, e.AssetID)
	}
	if e.AssetID == "" {
		log.Printf("[registry] warning: device %s has no asset_id", e.DeviceID)
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

	if e.AssetCoords != nil {
		if e.AssetCoords.Lat < -90 || e.AssetCoords.Lat > 90 {
			return fmt.Errorf("device %s: asset_coords.lat %f out of range [-90, 90]", e.DeviceID, e.AssetCoords.Lat)
		}
		if e.AssetCoords.Lng < -180 || e.AssetCoords.Lng > 180 {
			return fmt.Errorf("device %s: asset_coords.lng %f out of range [-180, 180]", e.DeviceID, e.AssetCoords.Lng)
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

		assetCoords := AssetCoords{}
		if entry.AssetCoords != nil {
			assetCoords = *entry.AssetCoords
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
			AssetID:           entry.AssetID,
			AssetCoords:       assetCoords,
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
// InfluxDB3 writer
// ─────────────────────────────────────────────────────────────────────────────

func writeSensorRecords(ctx context.Context, iot *influxdb3.Client, records []record.SensorDataRecord) error {
	if len(records) == 0 {
		return nil
	}
	points := make([]*influxdb3.Point, 0, len(records))
	for _, r := range records {
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
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := iot.WritePoints(ctx, points, influxdb3.WithNoSync(true))
		if err == nil {
			return nil
		}
		errStr := err.Error()
		// 4xx errors other than 429 (rate limit) are not recoverable – log & skip.
		if is4xx(errStr) && !strings.Contains(errStr, "429") {
			return err
		}
		if attempt < maxAttempts {
			backoff := time.Duration(attempt*attempt) * 250 * time.Millisecond
			log.Printf("[write] attempt %d/%d failed (%v) – retrying in %v", attempt, maxAttempts, err, backoff)
			time.Sleep(backoff)
		} else {
			return err
		}
	}
	return nil // unreachable
}

// is4xx reports whether an error string contains an HTTP 4xx status code.
func is4xx(errStr string) bool {
	for _, code := range []string{"400", "401", "403", "404", "405", "422"} {
		if strings.Contains(errStr, code) {
			return true
		}
	}
	return false
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
	if err := audit.WritePoints(ctx, []*influxdb3.Point{p}, influxdb3.WithNoSync(true)); err != nil {
		log.Printf("[audit] write error for %s: %v", deviceID, err)
	}
}

// writeCalibrations writes sensor_calibration entries for every explicit
// DeviceCalibration defined in the device registry.
// Called once at startup; entries act as a reference table for users plotting
// calibrated values with: calibrated = (raw ^ power) × scale + offset
func writeCalibrations(ctx context.Context, iot *influxdb3.Client, deviceMap map[string]DeviceConfig) {
	var points []*influxdb3.Point
	ts := time.Now()
	for deviceID, config := range deviceMap {
		for _, c := range config.Calibrations {
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
		log.Printf("[calibration] no custom calibrations defined — all devices use identity transform")
		return
	}
	if err := iot.WritePoints(ctx, points, influxdb3.WithNoSync(true)); err != nil {
		log.Printf("[calibration] write error: %v", err)
	} else {
		log.Printf("[calibration] wrote %d entries to sensor_calibration", len(points))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Payload dispatcher
// ─────────────────────────────────────────────────────────────────────────────

func b64ToByte(b64 string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(b64)
}

// parseDeviceModel decodes rawPayload and writes sensor_data points to InfluxDB3.
//
// provider == "custom": rawPayload is raw JSON; timestamp comes from the message.
// LNS providers:        rawPayload is base64-encoded binary; timestamp from LNS frame.
func parseDeviceModel(ctx context.Context, iot *influxdb3.Client,
	deviceID, devEUI string, config DeviceConfig, provider, rawPayload string, port uint64, ts time.Time) {

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
	for i := range records {
		records[i].DevEUI = devEUI
		records[i].MacAddress = config.MacAddress
	}
	if err := writeSensorRecords(ctx, iot, records); err != nil {
		log.Printf("[parse] write error for %s: %v", deviceID, err)
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
func parseMsg(ctx context.Context, iot *influxdb3.Client, provider, deviceID, devEUI string, config DeviceConfig, message string) {
	if message == "" {
		return
	}
	switch provider {

	case "custom":
		// Direct MQTT device: raw JSON, no LNS frame wrapping.
		parseDeviceModel(ctx, iot, deviceID, "", config, "custom", message, 0, time.Time{})

	case "chirpstackv4":
		frame, err := chirpstack.Parse(message)
		if err != nil {
			log.Printf("[parseMsg/chirpstackv4] %v (deviceID=%s)", err, deviceID)
			return
		}
		parseDeviceModel(ctx, iot, deviceID, devEUI, config, "chirpstackv4", frame.Data, frame.Port, frame.Timestamp)

	case "everynet":
		frame, err := everynet.Parse(message)
		if err != nil {
			log.Printf("[parseMsg/everynet] %v (deviceID=%s)", err, deviceID)
			return
		}
		parseDeviceModel(ctx, iot, deviceID, devEUI, config, "everynet", frame.Data, frame.Port, frame.Timestamp)

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
			registryMu.Lock()
			deviceMap = nextDeviceMap
			devEUIToDeviceID = nextDevEUIToDeviceID
			registryMu.Unlock()
			log.Printf("[registry] reloaded: file=%s deviceIDs=%d devEUI mappings=%d", DEVICE_REGISTRY_FILE, len(nextDeviceMap), len(nextDevEUIToDeviceID))
		}
	}()

	id := uuid.New().String()
	mqttOpts := MQTT.NewClientOptions().
		AddBroker(MQTT_BROKER).
		SetClientID("parse-lns-sub-" + id).
		SetUsername("public").
		SetPassword("public").
		SetConnectionLostHandler(connLostHandler)

	incoming := make(chan [2]string, 64)
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

	// Main loop ──────────────────────────────────────────────────────────────
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

		// Write raw payload to audit_iot before any decoding.
		writeAuditLog(ctx, clients.AuditIoT, deviceID, payload, time.Now())

		if devEUI != "" {
			log.Printf("[recv] topic=%s provider=%s device_id=%s dev_eui=%s model=%s", topic, provider, deviceID, devEUI, config.Model)
		} else {
			log.Printf("[recv] topic=%s provider=%s device_id=%s model=%s", topic, provider, deviceID, config.Model)
		}
		parseMsg(ctx, clients.IoTSensors, provider, deviceID, devEUI, config, payload)
	}
}
