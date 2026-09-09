package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestGetDevicesMap_NoLongerReadsAssetFields(t *testing.T) {
	path := writeTempFile(t, "devices.json", `{
		"version": "1",
		"devices": [
			{
				"device_id": "019b9ae2-fc84-7396-9ea8-fd2a041b7664",
				"device_model": "em500_swl",
				"device_type": "lorawan",
				"serial_number": "SN-1",
				"dev_eui": "24e124126d284622"
			}
		]
	}`)

	deviceMap, devEUIToDeviceID, err := GetDevicesMap(path)
	if err != nil {
		t.Fatalf("GetDevicesMap: %v", err)
	}
	if len(deviceMap) != 1 || len(devEUIToDeviceID) != 1 {
		t.Fatalf("expected 1 device, got deviceMap=%d devEUI=%d", len(deviceMap), len(devEUIToDeviceID))
	}
	cfg := deviceMap["019b9ae2-fc84-7396-9ea8-fd2a041b7664"]
	if cfg.AssetID != "" || cfg.AssetCoords != (AssetCoords{}) {
		t.Errorf("expected zero-value asset info from devices.json alone, got %+v", cfg)
	}
}

func TestGetAssetsMap_ResolvesDeviceIDs(t *testing.T) {
	path := writeTempFile(t, "assets.json", `{
		"version": "1",
		"assets": [
			{
				"asset_id": "019df4d6-f351-77a5-a3c2-926523251c47",
				"asset_coords": { "lat": -23.565, "lng": -46.655, "alt": 760 },
				"device_ids": [
					"019b9ae2-fc84-7396-9ea8-fd2a041b7664",
					"019c2f32-0637-7304-8bc3-bc5c58b2961c"
				]
			}
		]
	}`)

	assetByDeviceID, err := GetAssetsMap(path)
	if err != nil {
		t.Fatalf("GetAssetsMap: %v", err)
	}
	if len(assetByDeviceID) != 2 {
		t.Fatalf("expected 2 device links, got %d", len(assetByDeviceID))
	}
	for _, deviceID := range []string{
		"019b9ae2-fc84-7396-9ea8-fd2a041b7664",
		"019c2f32-0637-7304-8bc3-bc5c58b2961c",
	} {
		cfg, ok := assetByDeviceID[deviceID]
		if !ok {
			t.Fatalf("device %s not resolved to an asset", deviceID)
		}
		if cfg.AssetID != "019df4d6-f351-77a5-a3c2-926523251c47" {
			t.Errorf("device %s: got asset_id %s", deviceID, cfg.AssetID)
		}
		if cfg.AssetCoords.Lat != -23.565 || cfg.AssetCoords.Lng != -46.655 || cfg.AssetCoords.Alt != 760 {
			t.Errorf("device %s: got coords %+v", deviceID, cfg.AssetCoords)
		}
	}
}

func TestGetAssetsMap_RejectsDuplicateAssetID(t *testing.T) {
	path := writeTempFile(t, "assets.json", `{
		"version": "1",
		"assets": [
			{"asset_id": "019df4d6-f351-77a5-a3c2-926523251c47", "device_ids": []},
			{"asset_id": "019df4d6-f351-77a5-a3c2-926523251c47", "device_ids": []}
		]
	}`)

	if _, err := GetAssetsMap(path); err == nil {
		t.Fatal("expected error for duplicate asset_id, got nil")
	}
}

func TestMergeAssetInfo_BackfillsDeviceConfig(t *testing.T) {
	deviceMap := map[string]DeviceConfig{
		"dev-1": {Model: "EM500_SWL"},
		"dev-2": {Model: "DTL200"},
	}
	assetByDeviceID := map[string]AssetConfig{
		"dev-1": {AssetID: "asset-1", AssetCoords: AssetCoords{Lat: 1, Lng: 2, Alt: 3}},
	}

	mergeAssetInfo(deviceMap, assetByDeviceID)

	if deviceMap["dev-1"].AssetID != "asset-1" {
		t.Errorf("dev-1: expected asset-1, got %q", deviceMap["dev-1"].AssetID)
	}
	if deviceMap["dev-2"].AssetID != "" {
		t.Errorf("dev-2: expected no asset link, got %q", deviceMap["dev-2"].AssetID)
	}
}

// ─── sensors.json ──────────────────────────────────────────────────────────

const (
	testDeviceIDUC300 = "01a03953-9387-7631-b960-5d7f9305a67c"
	testSensorID1     = "01a03953-9387-76dd-bc9f-ee2b674fed66"
	testSensorID2     = "01a03953-9387-76e9-b9dd-ce9662f2e172"
	testSensorID3     = "01a03953-9387-76f5-85c6-fc9899b5ecc8"
	testDeviceIDUC100 = "01a03953-9387-7700-ac0e-cd3a0d14d2f0"
	testSensorID4     = "01a03953-9387-7708-9591-45b08aed9596"
)

func TestGetSensorsMap_LoadsAndValidates(t *testing.T) {
	path := writeTempFile(t, "sensors.json", `{
		"version": "1",
		"sensors": [
			{"sensor_id": "`+testSensorID1+`", "sensor_type": "water_level", "device_id": "`+testDeviceIDUC300+`"},
			{"sensor_id": "`+testSensorID2+`", "sensor_type": "battery_level", "device_id": "`+testDeviceIDUC300+`"}
		]
	}`)

	sensorMap, err := GetSensorsMap(path)
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
	path := writeTempFile(t, "sensors.json", `{
		"version": "1",
		"sensors": [
			{"sensor_id": "`+testSensorID1+`", "sensor_type": "not_a_real_sensor_type", "device_id": "`+testDeviceIDUC300+`"}
		]
	}`)

	if _, err := GetSensorsMap(path); err == nil {
		t.Fatal("expected error for unrecognized sensor_type, got nil")
	}
}

func TestGetSensorsMap_AllowsSameSensorTypeTwiceOnOneDeviceViaDeviceIndex(t *testing.T) {
	// Two identical probes on one device — same sensor_type, distinct
	// sensor_id, disambiguated by device_index.
	path := writeTempFile(t, "sensors.json", `{
		"version": "1",
		"sensors": [
			{"sensor_id": "`+testSensorID1+`", "sensor_type": "soil_moisture_raw", "device_id": "`+testDeviceIDUC300+`", "device_index": 1},
			{"sensor_id": "`+testSensorID2+`", "sensor_type": "soil_moisture_raw", "device_id": "`+testDeviceIDUC300+`", "device_index": 2}
		]
	}`)

	sensorMap, err := GetSensorsMap(path)
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
	// two different sensor_ids — a genuine config error, not legitimate
	// multi-instance disambiguation.
	path := writeTempFile(t, "sensors.json", `{
		"version": "1",
		"sensors": [
			{"sensor_id": "`+testSensorID1+`", "sensor_type": "water_level", "device_id": "`+testDeviceIDUC300+`"},
			{"sensor_id": "`+testSensorID2+`", "sensor_type": "water_level", "device_id": "`+testDeviceIDUC300+`"}
		]
	}`)

	if _, err := GetSensorsMap(path); err == nil {
		t.Fatal("expected error for duplicate device_id/sensor_type/device_index tuple, got nil")
	}
}

func TestGetSensorsMap_LoadsSensorName(t *testing.T) {
	path := writeTempFile(t, "sensors.json", `{
		"version": "1",
		"sensors": [
			{"sensor_id": "`+testSensorID1+`", "sensor_type": "soil_moisture_raw", "device_id": "`+testDeviceIDUC300+`", "device_index": 1, "sensor_name": "Depth 10cm"}
		]
	}`)

	sensorMap, err := GetSensorsMap(path)
	if err != nil {
		t.Fatalf("GetSensorsMap: %v", err)
	}
	got := sensorMap[testSensorID1]
	if got.SensorName != "Depth 10cm" {
		t.Errorf("SensorName: got %q, want %q", got.SensorName, "Depth 10cm")
	}
}

func TestGetSensorsMap_RejectsDuplicateSensorID(t *testing.T) {
	path := writeTempFile(t, "sensors.json", `{
		"version": "1",
		"sensors": [
			{"sensor_id": "`+testSensorID1+`", "sensor_type": "water_level", "device_id": "`+testDeviceIDUC300+`"},
			{"sensor_id": "`+testSensorID1+`", "sensor_type": "battery_level", "device_id": "`+testDeviceIDUC300+`"}
		]
	}`)

	if _, err := GetSensorsMap(path); err == nil {
		t.Fatal("expected error for duplicate sensor_id, got nil")
	}
}

// ─── device_channels.json ──────────────────────────────────────────────────

func TestGetDeviceChannelsMap_ResolvesModbusAndIO(t *testing.T) {
	path := writeTempFile(t, "device_channels.json", `{
		"version": "1",
		"channels": [
			{"device_id": "`+testDeviceIDUC300+`", "sensor_id": "`+testSensorID1+`", "modbus_channel": 5, "modbus_slave_id": 2},
			{"device_id": "`+testDeviceIDUC300+`", "sensor_id": "`+testSensorID2+`", "io_channel": 9}
		]
	}`)

	dcMap, err := GetDeviceChannelsMap(path, nil)
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
	if dc.IO[9] != testSensorID2 {
		t.Errorf("io_channel 9: got %q, want %q", dc.IO[9], testSensorID2)
	}
}

func TestGetDeviceChannelsMap_RejectsUC100WithIO(t *testing.T) {
	path := writeTempFile(t, "device_channels.json", `{
		"version": "1",
		"channels": [
			{"device_id": "`+testDeviceIDUC100+`", "sensor_id": "`+testSensorID4+`", "io_channel": 9}
		]
	}`)
	deviceMap := map[string]DeviceConfig{
		testDeviceIDUC100: {Model: UC100},
	}

	if _, err := GetDeviceChannelsMap(path, deviceMap); err == nil {
		t.Fatal("expected error for io_channel on a uc100 device, got nil")
	}
}

func TestGetDeviceChannelsMap_AllowsIOWhenDeviceUnknown(t *testing.T) {
	// device_id not (yet) present in devices.json — should not block loading,
	// same "not yet resolvable" leniency as elsewhere in this file.
	path := writeTempFile(t, "device_channels.json", `{
		"version": "1",
		"channels": [
			{"device_id": "`+testDeviceIDUC100+`", "sensor_id": "`+testSensorID4+`", "io_channel": 9}
		]
	}`)

	if _, err := GetDeviceChannelsMap(path, map[string]DeviceConfig{}); err != nil {
		t.Fatalf("expected no error when device_id isn't in deviceMap, got: %v", err)
	}
}

func TestGetDeviceChannelsMap_RejectsBothModbusAndIO(t *testing.T) {
	path := writeTempFile(t, "device_channels.json", `{
		"version": "1",
		"channels": [
			{"device_id": "`+testDeviceIDUC300+`", "sensor_id": "`+testSensorID1+`", "modbus_channel": 5, "modbus_slave_id": 2, "io_channel": 9}
		]
	}`)

	if _, err := GetDeviceChannelsMap(path, nil); err == nil {
		t.Fatal("expected error for an entry set as both modbus and io, got nil")
	}
}

func TestGetDeviceChannelsMap_RejectsDuplicateChannelNumber(t *testing.T) {
	path := writeTempFile(t, "device_channels.json", `{
		"version": "1",
		"channels": [
			{"device_id": "`+testDeviceIDUC300+`", "sensor_id": "`+testSensorID1+`", "modbus_channel": 5, "modbus_slave_id": 2},
			{"device_id": "`+testDeviceIDUC300+`", "sensor_id": "`+testSensorID2+`", "modbus_channel": 5, "modbus_slave_id": 3}
		]
	}`)

	if _, err := GetDeviceChannelsMap(path, nil); err == nil {
		t.Fatal("expected error for two sensors claiming the same modbus_channel, got nil")
	}
}

func TestGetDeviceChannelsMap_RejectsDuplicateSensorID(t *testing.T) {
	path := writeTempFile(t, "device_channels.json", `{
		"version": "1",
		"channels": [
			{"device_id": "`+testDeviceIDUC300+`", "sensor_id": "`+testSensorID1+`", "modbus_channel": 5, "modbus_slave_id": 2},
			{"device_id": "`+testDeviceIDUC300+`", "sensor_id": "`+testSensorID1+`", "modbus_channel": 6, "modbus_slave_id": 2}
		]
	}`)

	if _, err := GetDeviceChannelsMap(path, nil); err == nil {
		t.Fatal("expected error for the same sensor_id claiming two channels, got nil")
	}
}

func TestGetDeviceChannelsMap_RejectsPartialModbusFields(t *testing.T) {
	path := writeTempFile(t, "device_channels.json", `{
		"version": "1",
		"channels": [
			{"device_id": "`+testDeviceIDUC300+`", "sensor_id": "`+testSensorID3+`", "modbus_channel": 5}
		]
	}`)

	if _, err := GetDeviceChannelsMap(path, nil); err == nil {
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
	// Channel 5 has no device_channels.json entry — must never be written
	// under a fabricated or empty sensor_type.
	r := &record.SensorDataRecord{ModbusChannel: 5}

	if ok := resolveChannelRecord(r, testDeviceIDUC300, r.ModbusChannel, reg.DeviceChannels[testDeviceIDUC300].Modbus, reg, "modbus"); ok {
		t.Fatal("expected resolution to fail for an unconfigured channel")
	}
	if r.SensorType != "" || r.SensorID != "" {
		t.Errorf("expected record left untouched, got SensorID=%q SensorType=%q", r.SensorID, r.SensorType)
	}
}

func TestResolveChannelRecord_DropsWhenSensorIDMissingFromSensorsJSON(t *testing.T) {
	// device_channels.json references a sensor_id, but sensors.json (loaded
	// separately, lenient FK) has no matching entry — must not fall back to
	// writing an empty sensor_type either.
	reg := registrySnapshot{
		DeviceChannels: map[string]DeviceChannels{
			testDeviceIDUC300: {Modbus: map[int]string{3: testSensorID1}},
		},
		SensorMap: map[string]SensorConfig{},
	}
	r := &record.SensorDataRecord{ModbusChannel: 3}

	if ok := resolveChannelRecord(r, testDeviceIDUC300, r.ModbusChannel, reg.DeviceChannels[testDeviceIDUC300].Modbus, reg, "modbus"); ok {
		t.Fatal("expected resolution to fail when sensor_id has no sensors.json entry")
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
