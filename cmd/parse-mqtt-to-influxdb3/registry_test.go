package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

// newRegistryTestServer starts an httptest server that returns body (a JSON
// array) for any request — every GetXMap fetcher only ever hits one fixed
// sub-path per test, so a single canned response is enough. Closed
// automatically via t.Cleanup.
func newRegistryTestServer(t *testing.T, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// ─── devices ────────────────────────────────────────────────────────────────

func TestGetDevicesMap_ParsesRowIncludingInlineAssetID(t *testing.T) {
	apiURL := newRegistryTestServer(t, `[
		{
			"id": "019b9ae2-fc84-7396-9ea8-fd2a041b7664",
			"device_model": "em500_swl",
			"device_type": "lorawan",
			"serial_number": "SN-1",
			"dev_eui": "24e124126d284622",
			"mac_address": null,
			"battery_voltage_max": null,
			"battery_voltage_min": null,
			"probe_range_m": null,
			"allow": true,
			"asset_id": "019df4d6-f351-77a5-a3c2-926523251c47"
		}
	]`)

	deviceMap, devEUIToDeviceID, err := GetDevicesMap(apiURL)
	if err != nil {
		t.Fatalf("GetDevicesMap: %v", err)
	}
	if len(deviceMap) != 1 || len(devEUIToDeviceID) != 1 {
		t.Fatalf("expected 1 device, got deviceMap=%d devEUI=%d", len(deviceMap), len(devEUIToDeviceID))
	}
	cfg := deviceMap["019b9ae2-fc84-7396-9ea8-fd2a041b7664"]
	// asset_id now comes straight from the device's own row — no separate
	// asset-registry cross-reference/merge step needed anymore.
	if cfg.AssetID != "019df4d6-f351-77a5-a3c2-926523251c47" {
		t.Errorf("expected asset_id inline from the device row, got %q", cfg.AssetID)
	}
	if cfg.SerialNumber != "SN-1" || cfg.DeviceType != "LORAWAN" {
		t.Errorf("got %+v", cfg)
	}
	if devEUIToDeviceID["24e124126d284622"] != "019b9ae2-fc84-7396-9ea8-fd2a041b7664" {
		t.Errorf("devEUI mapping missing or wrong: %+v", devEUIToDeviceID)
	}
}

func TestGetDevicesMap_SkipsDisallowedDevices(t *testing.T) {
	apiURL := newRegistryTestServer(t, `[
		{"id": "019b9ae2-fc84-7396-9ea8-fd2a041b7664", "device_model": "em500_swl", "device_type": "lorawan", "dev_eui": "24e124126d284622", "allow": false}
	]`)

	deviceMap, _, err := GetDevicesMap(apiURL)
	if err != nil {
		t.Fatalf("GetDevicesMap: %v", err)
	}
	if len(deviceMap) != 0 {
		t.Fatalf("expected disallowed device to be skipped, got %d devices", len(deviceMap))
	}
}

func TestGetDevicesMap_RejectsDuplicateDevEUI(t *testing.T) {
	apiURL := newRegistryTestServer(t, `[
		{"id": "019b9ae2-fc84-7396-9ea8-fd2a041b7664", "device_model": "em500_swl", "device_type": "lorawan", "dev_eui": "24e124126d284622", "allow": true},
		{"id": "019c2f32-0637-7304-8bc3-bc5c58b2961c", "device_model": "em500_swl", "device_type": "lorawan", "dev_eui": "24e124126d284622", "allow": true}
	]`)

	if _, _, err := GetDevicesMap(apiURL); err == nil {
		t.Fatal("expected error for duplicate dev_eui, got nil")
	}
}

// ─── assets ─────────────────────────────────────────────────────────────────

func TestGetAssetIDSet_ParsesRows(t *testing.T) {
	apiURL := newRegistryTestServer(t, `[
		{"id": "019df4d6-f351-77a5-a3c2-926523251c47"},
		{"id": "019df4d6-f351-77a5-a3c2-926523251c48"}
	]`)

	ids, err := GetAssetIDSet(apiURL)
	if err != nil {
		t.Fatalf("GetAssetIDSet: %v", err)
	}
	if len(ids) != 2 || !ids["019df4d6-f351-77a5-a3c2-926523251c47"] || !ids["019df4d6-f351-77a5-a3c2-926523251c48"] {
		t.Errorf("got %+v", ids)
	}
}

func TestValidateAssetReferences_HandlesEmptyAndMissingGracefully(t *testing.T) {
	// Purely a smoke test — this function only logs, never fails a reload;
	// it must not panic on nil/empty inputs or on a reference to an asset
	// that doesn't exist.
	validateAssetReferences(nil, nil, nil)
	validateAssetReferences(
		map[string]DeviceConfig{"dev-1": {AssetID: "missing-asset"}},
		map[string]SensorConfig{"sensor-1": {AssetID: "also-missing"}},
		map[string]bool{"real-asset": true},
	)
}

// ─── effectiveAssetID (sensor asset_id override vs device's own) ───────────

func TestEffectiveAssetID_SensorOverrideWins(t *testing.T) {
	got := effectiveAssetID("device-asset", SensorConfig{AssetID: "sensor-override-asset"})
	if got != "sensor-override-asset" {
		t.Errorf("got %q, want sensor override to win", got)
	}
}

func TestEffectiveAssetID_FallsBackToDeviceWhenSensorHasNoOverride(t *testing.T) {
	got := effectiveAssetID("device-asset", SensorConfig{})
	if got != "device-asset" {
		t.Errorf("got %q, want device's own asset_id", got)
	}
}

func TestEffectiveAssetID_BothEmptyYieldsEmpty(t *testing.T) {
	if got := effectiveAssetID("", SensorConfig{}); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// ─── sensors ────────────────────────────────────────────────────────────────

const (
	testDeviceIDUC300 = "01a03953-9387-7631-b960-5d7f9305a67c"
	testSensorID1     = "01a03953-9387-76dd-bc9f-ee2b674fed66"
	testSensorID2     = "01a03953-9387-76e9-b9dd-ce9662f2e172"
	testSensorID3     = "01a03953-9387-76f5-85c6-fc9899b5ecc8"
	testDeviceIDUC100 = "01a03953-9387-7700-ac0e-cd3a0d14d2f0"
	testSensorID4     = "01a03953-9387-7708-9591-45b08aed9596"
)

func TestGetSensorsMap_LoadsAndValidates(t *testing.T) {
	apiURL := newRegistryTestServer(t, `[
		{"id": "`+testSensorID1+`", "sensor_type": "water_level", "device_id": "`+testDeviceIDUC300+`"},
		{"id": "`+testSensorID2+`", "sensor_type": "battery_level", "device_id": "`+testDeviceIDUC300+`"}
	]`)

	sensorMap, err := GetSensorsMap(apiURL)
	if err != nil {
		t.Fatalf("GetSensorsMap: %v", err)
	}
	if len(sensorMap) != 2 {
		t.Fatalf("expected 2 sensors, got %d", len(sensorMap))
	}
	got := sensorMap[testSensorID1]
	if got.SensorType != "water_level" || got.DeviceID != testDeviceIDUC300 {
		t.Errorf("sensor 1: got %+v", got)
	}
}

func TestGetSensorsMap_RejectsUnknownSensorType(t *testing.T) {
	apiURL := newRegistryTestServer(t, `[
		{"id": "`+testSensorID1+`", "sensor_type": "not_a_real_sensor_type", "device_id": "`+testDeviceIDUC300+`"}
	]`)

	if _, err := GetSensorsMap(apiURL); err == nil {
		t.Fatal("expected error for unrecognized sensor_type, got nil")
	}
}

func TestGetSensorsMap_AllowsSameSensorTypeTwiceOnOneDeviceViaDeviceIndex(t *testing.T) {
	// Two identical probes on one device — same sensor_type, distinct
	// sensor_id, disambiguated by device_index.
	apiURL := newRegistryTestServer(t, `[
		{"id": "`+testSensorID1+`", "sensor_type": "soil_moisture_raw", "device_id": "`+testDeviceIDUC300+`", "device_index": 1},
		{"id": "`+testSensorID2+`", "sensor_type": "soil_moisture_raw", "device_id": "`+testDeviceIDUC300+`", "device_index": 2}
	]`)

	sensorMap, err := GetSensorsMap(apiURL)
	if err != nil {
		t.Fatalf("GetSensorsMap: %v", err)
	}
	if len(sensorMap) != 2 {
		t.Fatalf("expected 2 sensors (not unique on device_id+sensor_type alone), got %d", len(sensorMap))
	}
	if sensorMap[testSensorID1].DeviceIndex != 1 || sensorMap[testSensorID2].DeviceIndex != 2 {
		t.Errorf("device_index not preserved: got %+v / %+v", sensorMap[testSensorID1], sensorMap[testSensorID2])
	}
}

func TestGetSensorsMap_RejectsDuplicateDeviceIndexTuple(t *testing.T) {
	// Same device_id+sensor_type+device_index (both default to 0) claimed by
	// two different sensor ids — a genuine config error, not legitimate
	// multi-instance disambiguation.
	apiURL := newRegistryTestServer(t, `[
		{"id": "`+testSensorID1+`", "sensor_type": "water_level", "device_id": "`+testDeviceIDUC300+`"},
		{"id": "`+testSensorID2+`", "sensor_type": "water_level", "device_id": "`+testDeviceIDUC300+`"}
	]`)

	if _, err := GetSensorsMap(apiURL); err == nil {
		t.Fatal("expected error for duplicate device_id/sensor_type/device_index tuple, got nil")
	}
}

func TestGetSensorsMap_LoadsSensorNameAndAssetOverride(t *testing.T) {
	apiURL := newRegistryTestServer(t, `[
		{"id": "`+testSensorID1+`", "sensor_type": "soil_moisture_raw", "device_id": "`+testDeviceIDUC300+`", "device_index": 1, "sensor_name": "Depth 10cm", "asset_id": "019df4d6-f351-77a5-a3c2-926523251c47"}
	]`)

	sensorMap, err := GetSensorsMap(apiURL)
	if err != nil {
		t.Fatalf("GetSensorsMap: %v", err)
	}
	got := sensorMap[testSensorID1]
	if got.SensorName != "Depth 10cm" {
		t.Errorf("SensorName: got %q, want %q", got.SensorName, "Depth 10cm")
	}
	if got.AssetID != "019df4d6-f351-77a5-a3c2-926523251c47" {
		t.Errorf("AssetID override: got %q", got.AssetID)
	}
}

func TestGetSensorsMap_RejectsDuplicateSensorID(t *testing.T) {
	apiURL := newRegistryTestServer(t, `[
		{"id": "`+testSensorID1+`", "sensor_type": "water_level", "device_id": "`+testDeviceIDUC300+`"},
		{"id": "`+testSensorID1+`", "sensor_type": "battery_level", "device_id": "`+testDeviceIDUC300+`"}
	]`)

	if _, err := GetSensorsMap(apiURL); err == nil {
		t.Fatal("expected error for duplicate sensor id, got nil")
	}
}

// ─── modbus-channels ────────────────────────────────────────────────────────

func TestGetDeviceChannelsMap_ResolvesModbus(t *testing.T) {
	apiURL := newRegistryTestServer(t, `[
		{"id": "f7f2e152-df25-499c-9ca5-c458bb8a8a37", "device_id": "`+testDeviceIDUC300+`", "bus_type": "modbus_rs485", "sensor_id": "`+testSensorID1+`", "modbus_channel": 5, "modbus_slave_id": 2}
	]`)

	dcMap, err := GetDeviceChannelsMap(apiURL)
	if err != nil {
		t.Fatalf("GetDeviceChannelsMap: %v", err)
	}
	dc, ok := dcMap[testDeviceIDUC300]
	if !ok {
		t.Fatalf("device %s not found", testDeviceIDUC300)
	}
	if dc.Modbus[5] != testSensorID1 {
		t.Errorf("modbus_channel 5: got %q, want %q", dc.Modbus[5], testSensorID1)
	}
	// The registry API has no io-channels endpoint yet — IO must always come
	// back empty, never nil (so callers can index it unconditionally).
	if dc.IO == nil || len(dc.IO) != 0 {
		t.Errorf("expected IO to be an empty (non-nil) map, got %+v", dc.IO)
	}
}

func TestGetDeviceChannelsMap_RejectsDuplicateChannelNumber(t *testing.T) {
	apiURL := newRegistryTestServer(t, `[
		{"id": "a", "device_id": "`+testDeviceIDUC300+`", "sensor_id": "`+testSensorID1+`", "modbus_channel": 5, "modbus_slave_id": 2},
		{"id": "b", "device_id": "`+testDeviceIDUC300+`", "sensor_id": "`+testSensorID2+`", "modbus_channel": 5, "modbus_slave_id": 3}
	]`)

	if _, err := GetDeviceChannelsMap(apiURL); err == nil {
		t.Fatal("expected error for two sensors claiming the same modbus_channel, got nil")
	}
}

func TestGetDeviceChannelsMap_RejectsDuplicateSensorID(t *testing.T) {
	apiURL := newRegistryTestServer(t, `[
		{"id": "a", "device_id": "`+testDeviceIDUC300+`", "sensor_id": "`+testSensorID1+`", "modbus_channel": 5, "modbus_slave_id": 2},
		{"id": "b", "device_id": "`+testDeviceIDUC300+`", "sensor_id": "`+testSensorID1+`", "modbus_channel": 6, "modbus_slave_id": 2}
	]`)

	if _, err := GetDeviceChannelsMap(apiURL); err == nil {
		t.Fatal("expected error for the same sensor_id claiming two channels, got nil")
	}
}

func TestGetDeviceChannelsMap_RejectsMissingModbusSlaveID(t *testing.T) {
	apiURL := newRegistryTestServer(t, `[
		{"id": "a", "device_id": "`+testDeviceIDUC300+`", "sensor_id": "`+testSensorID3+`", "modbus_channel": 5, "modbus_slave_id": 0}
	]`)

	if _, err := GetDeviceChannelsMap(apiURL); err == nil {
		t.Fatal("expected error for modbus_channel without modbus_slave_id, got nil")
	}
}

// ─── buildSensorIndex ──────────────────────────────────────────────────────

func TestBuildSensorIndex_ResolvesUniquePairs(t *testing.T) {
	sensorMap := map[string]SensorConfig{
		testSensorID1: {SensorType: "water_level", DeviceID: testDeviceIDUC300},
		testSensorID4: {SensorType: "battery_level", DeviceID: testDeviceIDUC100},
	}

	index := buildSensorIndex(sensorMap)
	if len(index) != 2 {
		t.Fatalf("expected 2 index entries, got %d", len(index))
	}
	if got := index[sensorIndexKey(testDeviceIDUC300, "water_level", 0)]; got != testSensorID1 {
		t.Errorf("water_level lookup: got %q, want %q", got, testSensorID1)
	}
	if got := index[sensorIndexKey(testDeviceIDUC100, "battery_level", 0)]; got != testSensorID4 {
		t.Errorf("battery_level lookup: got %q, want %q", got, testSensorID4)
	}
}

func TestBuildSensorIndex_SkipsAmbiguousPairs(t *testing.T) {
	// Two instruments sharing device_id+sensor_type+device_index (both 0,
	// i.e. neither sets one) can't be told apart — must be left out of the
	// index entirely. GetSensorsMap would normally reject this at load time
	// (duplicate tuple); buildSensorIndex doesn't re-trust that, so this
	// exercises its own defensive skip.
	sensorMap := map[string]SensorConfig{
		testSensorID1: {SensorType: "water_level", DeviceID: testDeviceIDUC300},
		testSensorID2: {SensorType: "water_level", DeviceID: testDeviceIDUC300},
		testSensorID4: {SensorType: "battery_level", DeviceID: testDeviceIDUC100},
	}

	index := buildSensorIndex(sensorMap)
	if len(index) != 1 {
		t.Fatalf("expected only the unambiguous pair to be indexed, got %d entries: %+v", len(index), index)
	}
	if _, ok := index[sensorIndexKey(testDeviceIDUC300, "water_level", 0)]; ok {
		t.Error("expected ambiguous water_level pair to be excluded from index")
	}
	if got := index[sensorIndexKey(testDeviceIDUC100, "battery_level", 0)]; got != testSensorID4 {
		t.Errorf("battery_level lookup: got %q, want %q", got, testSensorID4)
	}
}

func TestBuildSensorIndex_DeviceIndexDisambiguatesSharedSensorType(t *testing.T) {
	// Three soil-moisture probes on one device, same sensor_type, told apart
	// by device_index — this is exactly the case DeviceIndex exists for.
	sensorMap := map[string]SensorConfig{
		testSensorID1: {SensorType: "soil_moisture_raw", DeviceID: testDeviceIDUC300, DeviceIndex: 1},
		testSensorID2: {SensorType: "soil_moisture_raw", DeviceID: testDeviceIDUC300, DeviceIndex: 2},
		testSensorID3: {SensorType: "soil_moisture_raw", DeviceID: testDeviceIDUC300, DeviceIndex: 3},
	}

	index := buildSensorIndex(sensorMap)
	if len(index) != 3 {
		t.Fatalf("expected all 3 to resolve uniquely via device_index, got %d entries: %+v", len(index), index)
	}
	cases := []struct {
		deviceIndex int
		want        string
	}{
		{1, testSensorID1}, {2, testSensorID2}, {3, testSensorID3},
	}
	for _, c := range cases {
		if got := index[sensorIndexKey(testDeviceIDUC300, "soil_moisture_raw", c.deviceIndex)]; got != c.want {
			t.Errorf("device_index %d: got %q, want %q", c.deviceIndex, got, c.want)
		}
	}
}

// ─── resolveChannelRecord (UC100/UC300 channel -> sensor resolution) ───────

func TestResolveChannelRecord_ResolvesConfiguredModbusChannel(t *testing.T) {
	reg := registrySnapshot{
		DeviceChannels: map[string]DeviceChannels{
			testDeviceIDUC300: {Modbus: map[int]string{3: testSensorID1}},
		},
		SensorMap: map[string]SensorConfig{
			testSensorID1: {SensorType: "water_level", DeviceID: testDeviceIDUC300},
		},
	}
	r := &record.SensorDataRecord{ModbusChannel: 3}

	if ok := resolveChannelRecord(r, testDeviceIDUC300, r.ModbusChannel, reg.DeviceChannels[testDeviceIDUC300].Modbus, reg, "modbus"); !ok {
		t.Fatal("expected resolution to succeed")
	}
	if r.SensorID != testSensorID1 || r.SensorType != "water_level" {
		t.Errorf("got SensorID=%q SensorType=%q, want %q/%q", r.SensorID, r.SensorType, testSensorID1, "water_level")
	}
}

func TestResolveChannelRecord_ResolvesConfiguredIOChannel(t *testing.T) {
	// UC300's fixed GPIO/PT100/ADC hardware resolves the same way as Modbus —
	// channel number -> sensor_id -> sensor_type, never a name invented in Go.
	reg := registrySnapshot{
		DeviceChannels: map[string]DeviceChannels{
			testDeviceIDUC300: {IO: map[int]string{9: testSensorID2}},
		},
		SensorMap: map[string]SensorConfig{
			testSensorID2: {SensorType: "battery_level", DeviceID: testDeviceIDUC300},
		},
	}
	r := &record.SensorDataRecord{IOChannel: 9}

	if ok := resolveChannelRecord(r, testDeviceIDUC300, r.IOChannel, reg.DeviceChannels[testDeviceIDUC300].IO, reg, "io"); !ok {
		t.Fatal("expected resolution to succeed")
	}
	if r.SensorID != testSensorID2 || r.SensorType != "battery_level" {
		t.Errorf("got SensorID=%q SensorType=%q, want %q/%q", r.SensorID, r.SensorType, testSensorID2, "battery_level")
	}
}

func TestResolveChannelRecord_DropsUnconfiguredChannel(t *testing.T) {
	reg := registrySnapshot{
		DeviceChannels: map[string]DeviceChannels{
			testDeviceIDUC300: {Modbus: map[int]string{3: testSensorID1}},
		},
		SensorMap: map[string]SensorConfig{
			testSensorID1: {SensorType: "water_level", DeviceID: testDeviceIDUC300},
		},
	}
	// Channel 5 has no modbus-channels registry entry — must never be
	// written under a fabricated or empty sensor_type.
	r := &record.SensorDataRecord{ModbusChannel: 5}

	if ok := resolveChannelRecord(r, testDeviceIDUC300, r.ModbusChannel, reg.DeviceChannels[testDeviceIDUC300].Modbus, reg, "modbus"); ok {
		t.Fatal("expected resolution to fail for an unconfigured channel")
	}
	if r.SensorType != "" || r.SensorID != "" {
		t.Errorf("expected record left untouched, got SensorID=%q SensorType=%q", r.SensorID, r.SensorType)
	}
}

func TestResolveChannelRecord_DropsWhenSensorIDMissingFromSensorRegistry(t *testing.T) {
	// device_channels registry references a sensor_id, but the sensor
	// registry (loaded separately, lenient FK) has no matching entry — must
	// not fall back to writing an empty sensor_type either.
	reg := registrySnapshot{
		DeviceChannels: map[string]DeviceChannels{
			testDeviceIDUC300: {Modbus: map[int]string{3: testSensorID1}},
		},
		SensorMap: map[string]SensorConfig{},
	}
	r := &record.SensorDataRecord{ModbusChannel: 3}

	if ok := resolveChannelRecord(r, testDeviceIDUC300, r.ModbusChannel, reg.DeviceChannels[testDeviceIDUC300].Modbus, reg, "modbus"); ok {
		t.Fatal("expected resolution to fail when sensor_id has no sensor registry entry")
	}
	if r.SensorType != "" || r.SensorID != "" {
		t.Errorf("expected record left untouched, got SensorID=%q SensorType=%q", r.SensorID, r.SensorType)
	}
}

func TestResolveChannelRecord_UnknownDeviceHasNoChannels(t *testing.T) {
	reg := registrySnapshot{
		DeviceChannels: map[string]DeviceChannels{},
		SensorMap:      map[string]SensorConfig{},
	}
	r := &record.SensorDataRecord{ModbusChannel: 3}

	if ok := resolveChannelRecord(r, testDeviceIDUC300, r.ModbusChannel, reg.DeviceChannels[testDeviceIDUC300].Modbus, reg, "modbus"); ok {
		t.Fatal("expected resolution to fail for a device with no channel entries at all")
	}
}
