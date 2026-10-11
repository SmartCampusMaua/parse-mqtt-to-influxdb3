package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// rawInfraFile mirrors infra.json's on-disk shape. influxdb.orgs matches the
// SmartCampusMaua `api` repo's own influxdb.json format exactly (see that
// repo's src/config.ts), "allow" included — even though this service only
// ever selects one entry from it (the first not explicitly disabled), since
// it only ever talks to one InfluxDB host+token pair, shared across its
// four hardcoded databases. Keeping the on-disk shape identical to api's
// means the same file (or the same tooling that generates one) can feed
// both services without translation.
type rawInfraFile struct {
	InfluxDB struct {
		Orgs []struct {
			Org   string `json:"org"`
			Host  string `json:"host"`
			Token string `json:"token"`
			Allow *bool  `json:"allow"`
		} `json:"orgs"`
	} `json:"influxdb"`
	MQTT struct {
		Broker   string `json:"broker"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"mqtt"`
}

// InfraConfig holds this service's two external connection targets —
// InfluxDB3 (one selected org from infra.json's orgs list) and MQTT —
// loaded from a single JSON file instead of env vars, mirroring the
// SmartCampusMaua `api` repo's influxdb.json pattern: connection
// details/secrets live in a file that can be swapped per deployment
// (bind-mounted in docker-compose, mounted from a k8s ConfigMap/Secret)
// without baking them into env vars or the image.
type InfraConfig struct {
	InfluxDB struct {
		Org   string
		Host  string
		Token string
	}
	MQTT struct {
		Broker   string
		Username string
		Password string
	}
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
	var file rawInfraFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return InfraConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}

	var cfg InfraConfig
	for _, o := range file.InfluxDB.Orgs {
		if o.Allow != nil && !*o.Allow {
			continue
		}
		cfg.InfluxDB.Org = o.Org
		cfg.InfluxDB.Host = o.Host
		cfg.InfluxDB.Token = o.Token
		break
	}
	cfg.MQTT.Broker = file.MQTT.Broker
	cfg.MQTT.Username = file.MQTT.Username
	cfg.MQTT.Password = file.MQTT.Password

	switch {
	case cfg.InfluxDB.Host == "":
		return InfraConfig{}, fmt.Errorf("%s: influxdb.orgs has no enabled entry with a host", path)
	case cfg.InfluxDB.Token == "":
		return InfraConfig{}, fmt.Errorf("%s: influxdb.orgs has no enabled entry with a token", path)
	case cfg.MQTT.Broker == "":
		return InfraConfig{}, fmt.Errorf("%s: mqtt.broker is required", path)
	case cfg.MQTT.Username == "":
		return InfraConfig{}, fmt.Errorf("%s: mqtt.username is required", path)
	case cfg.MQTT.Password == "":
		return InfraConfig{}, fmt.Errorf("%s: mqtt.password is required", path)
	}
	return cfg, nil
}
