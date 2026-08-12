package main

import (
	"os"
	"path/filepath"
	"testing"
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
