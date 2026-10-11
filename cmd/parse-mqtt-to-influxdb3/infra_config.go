package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// InfraConfig holds this service's two external connection targets —
// InfluxDB3 and MQTT — loaded from a single JSON file instead of env vars,
// mirroring the SmartCampusMaua `api` repo's influxdb.json pattern (see that
// repo's src/config.ts): connection details/secrets live in a file that can
// be swapped per deployment (bind-mounted in docker-compose, mounted from a
// k8s Secret) without baking them into env vars or the image. Unlike that
// repo's influxdb.json, there is no "orgs" list here — this service only
// ever talks to one InfluxDB host+token pair (shared across its four
// hardcoded databases), so a flat object is all it needs.
type InfraConfig struct {
	InfluxDB struct {
		Host  string `json:"host"`
		Token string `json:"token"`
	} `json:"influxdb"`
	MQTT struct {
		Broker   string `json:"broker"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"mqtt"`
}

// loadInfraConfig reads and validates the infra config file. Called once at
// startup (fail fast on a bad/missing file: better to crash at boot than run
// with an empty token or broker address), and again on a timer in main() —
// see infraChanged there. Unlike the device/asset/sensor registry, which
// swaps its data in place on a reload, the InfluxDB3 clients and MQTT
// connection built from this config can't be hot-swapped safely, so a
// detected change there triggers a full graceful process restart instead of
// an in-place update; this function itself has no notion of that, it just
// reads and validates whatever's at path right now.
func loadInfraConfig(path string) (InfraConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return InfraConfig{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg InfraConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return InfraConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	switch {
	case cfg.InfluxDB.Host == "":
		return InfraConfig{}, fmt.Errorf("%s: influxdb.host is required", path)
	case cfg.InfluxDB.Token == "":
		return InfraConfig{}, fmt.Errorf("%s: influxdb.token is required", path)
	case cfg.MQTT.Broker == "":
		return InfraConfig{}, fmt.Errorf("%s: mqtt.broker is required", path)
	case cfg.MQTT.Username == "":
		return InfraConfig{}, fmt.Errorf("%s: mqtt.username is required", path)
	case cfg.MQTT.Password == "":
		return InfraConfig{}, fmt.Errorf("%s: mqtt.password is required", path)
	}
	return cfg, nil
}
