package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	influxdb3 "github.com/InfluxCommunity/influxdb3-go/v2/influxdb3"
	MQTT "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/dragino"
	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/khomp"
	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/kron"
	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/milesight"
	"github.com/OpenDataTelemetry/device-gateway-mqtt/record"
)

// DeviceModel is a typed string identifying the hardware model of a device.
// It is the sole routing key for payload decoding; fPort numbers are not used.
type DeviceModel string

const (
	EM500_SWL      DeviceModel = "EM500_SWL"
	KS3000_LORA    DeviceModel = "KS3000_LORA"
	KS3000_WIFI    DeviceModel = "KS3000_WIFI"
	WS101          DeviceModel = "WS101"
	DTL200_SWL     DeviceModel = "DTL200_SWL"
	EM300_DI       DeviceModel = "EM300_DI"
	NIT21LI_EMW104 DeviceModel = "NIT21LI_EMW104"
)

// GetDevicesMap returns the authoritative device-ID → DeviceModel registry.
// Messages from device IDs not listed here are silently dropped.
// Add new devices here to enable their processing.
func GetDevicesMap() map[string]DeviceModel {
	return map[string]DeviceModel{
		// ── DTL200-SWL (Dragino water level sensor) ─────────────────────────
		"a8404123415f13fe": DTL200_SWL,
		"a8404188945f13dd": DTL200_SWL,
		// ── EM300-DI (Milesight digital input / pulse counter) ──────────────
		"24e124136f315508": EM300_DI,
		"24e124136f483595": EM300_DI,
		"24e124136f484497": EM300_DI,
		"24e124136f484616": EM300_DI,
		// ── EM500-SWL (Milesight soil / water level sensor) ─────────────────
		"24e124126d284622": EM500_SWL,
		"24e124126f422301": EM500_SWL,
		"24e124126f422693": EM500_SWL,
		"24e124126f427639": EM500_SWL,
		"24e124126f427690": EM500_SWL,
		"24e124126f422141": EM500_SWL,
		"24e124126f427556": EM500_SWL,
		"24e124126f427781": EM500_SWL,
		"24e124126f427831": EM500_SWL,
		// ── KS3000-LoRa (Kron power meter via LoRaWAN) ──────────────────────
		"303331395230870e": KS3000_LORA,
		"303331396b30600f": KS3000_LORA,
		"303331396b30720e": KS3000_LORA,
		"303331396c30700e": KS3000_LORA,
		"303331396d305f0f": KS3000_LORA,
		"303331397230790e": KS3000_LORA,
		"3033313980307b0e": KS3000_LORA,
		// ── WS101-R (Milesight smart button) ────────────────────────────────
		"24e124535f318437": WS101,
		// ── NIT21LI-EMW104 (Khomp weather station, LoRaWAN fPort 4) ─────────
		"f803320100028a5f": NIT21LI_EMW104,
		"f803320100030977": NIT21LI_EMW104,
		"f8033201000357e1": NIT21LI_EMW104,
		"f8033201000385fa": NIT21LI_EMW104,
		// ── KS3000-WiFi (Kron power meter via direct MQTT) ───────────────────
		"019b08df-26e7-7506-a5f6-916b2bef24f4": KS3000_WIFI,
		"019b08df-26e7-71b5-8df6-56c2e954ac91": KS3000_WIFI,
		"019b08df-26e7-7119-97da-523f9236db80": KS3000_WIFI,
		"019b08df-26e7-7f7b-a18f-b3d6c3fdf248": KS3000_WIFI,
		"019b08df-26e7-7866-a0fb-1768122b8584": KS3000_WIFI,
	}
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

func writeSensorRecords(ctx context.Context, client *influxdb3.Client, records []record.SensorDataRecord) error {
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
		err := client.WritePoints(ctx, points, influxdb3.WithNoSync(true))
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
func parseDeviceModel(ctx context.Context, client *influxdb3.Client,
	deviceID string, deviceModel DeviceModel, provider, rawPayload string, port uint64, ts time.Time) {

	if rawPayload == "" {
		log.Printf("[parse] empty payload for deviceID=%s", deviceID)
		return
	}

	var records []record.SensorDataRecord

	if provider == "custom" {
		// Custom path: raw JSON, decoder extracts its own timestamp.
		switch deviceModel {
		case KS3000_WIFI:
			records = kron.DecodeWiFi(rawPayload, deviceID, strings.ToLower(string(deviceModel)))
		default:
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
		switch deviceModel {
		case NIT21LI_EMW104:
			records = khomp.DecodeNIT21LIEMW104(b, deviceID, provider, ts)
		case KS3000_LORA:
			records = kron.DecodeKS300LORA(b, deviceID, provider, ts)
		case EM300_DI:
			records = milesight.DecodeEM300DI(b, deviceID, provider, ts)
		case EM500_SWL:
			records = milesight.DecodeEM500SWL(b, deviceID, provider, ts)
		case DTL200_SWL:
			records = dragino.DecodeDTL200SWL(b, deviceID, provider, port, ts)
		case WS101:
			records = milesight.DecodeWS101(b, deviceID, provider, ts)
		default:
			log.Printf("[parse] no LNS decoder for device model %s", deviceModel)
			return
		}
	}

	if len(records) == 0 {
		log.Printf("[parse] decoder returned 0 records for %s (%s)", deviceID, deviceModel)
		return
	}
	if err := writeSensorRecords(ctx, client, records); err != nil {
		log.Printf("[parse] write error for %s: %v", deviceID, err)
	} else {
		log.Printf("[influxdb3] wrote %d points: device_model=%s device_id=%s",
			len(records), strings.ToLower(string(deviceModel)), deviceID)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Message entry point – single provider switch, no redundant checks
// ─────────────────────────────────────────────────────────────────────────────

// parseMsg routes an incoming MQTT message to the correct decoder.
// All provider detection and LNS frame parsing happen in one switch statement.
func parseMsg(ctx context.Context, client *influxdb3.Client, deviceID string, deviceModel DeviceModel, message string) {
	if message == "" {
		return
	}
	switch detectProvider(message) {

	case "custom":
		// Direct MQTT: raw JSON payload, decoder extracts its own timestamp.
		parseDeviceModel(ctx, client, deviceID, deviceModel, "custom", message, 0, time.Time{})

	case "chirpstackv4":
		// Minimal parse: only extract data payload and frame timestamp.
		var msg struct {
			FPort  uint64 `json:"fPort"`
			RxInfo []struct {
				NsTime time.Time `json:"nsTime"`
			} `json:"rxInfo"`
			Data string `json:"data"`
		}
		if err := json.Unmarshal([]byte(message), &msg); err != nil {
			log.Printf("[parseMsg/chirpstackv4] parse error for deviceID=%s: %v", deviceID, err)
			return
		}
		if len(msg.RxInfo) == 0 {
			log.Printf("[parseMsg/chirpstackv4] no rxInfo for deviceID=%s", deviceID)
			return
		}
		parseDeviceModel(ctx, client, deviceID, deviceModel, "chirpstackv4", msg.Data, msg.FPort, msg.RxInfo[0].NsTime)

	case "everynet":
		// Everynet compacts whitespace; strip before unmarshalling.
		var msg struct {
			Type   string `json:"type"`
			Params struct {
				Payload string  `json:"payload"`
				RxTime  float64 `json:"rx_time"`
				Port    uint64  `json:"port"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(strings.ReplaceAll(message, " ", "")), &msg); err != nil {
			log.Printf("[parseMsg/everynet] parse error for deviceID=%s: %v", deviceID, err)
			return
		}
		if msg.Type != "uplink" {
			log.Printf("[parseMsg/everynet] skipping type=%s for deviceID=%s", msg.Type, deviceID)
			return
		}
		// rx_time is a Unix float in seconds (e.g. 1774967267.485…).
		// Convert via integer seconds + fractional nanoseconds to avoid float64
		// precision loss and to produce time.Now() as a safe fallback.
		var ts time.Time
		if msg.Params.RxTime > 0 {
			sec := int64(msg.Params.RxTime)
			nsec := int64((msg.Params.RxTime - float64(sec)) * 1e9)
			ts = time.Unix(sec, nsec).UTC()
		} else {
			log.Printf("[parseMsg/everynet] missing rx_time for deviceID=%s, using now", deviceID)
			ts = time.Now().UTC()
		}
		parseDeviceModel(ctx, client, deviceID, deviceModel, "everynet", msg.Params.Payload, msg.Params.Port, ts)

	default:
		log.Printf("[parseMsg] cannot detect provider for deviceID=%s (%.80s…)", deviceID, message)
	}
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
	INFLUXDB_DATABASE := os.Getenv("INFLUXDB_DATABASE")
	if INFLUXDB_DATABASE == "" {
		INFLUXDB_DATABASE = "iot_rp40d"
	}
	INFLUXDB_ORG := os.Getenv("INFLUXDB_ORG")
	if INFLUXDB_ORG == "" {
		INFLUXDB_ORG = "IMT"
	}

	ctx := context.Background()

	influxClient, err := influxdb3.New(influxdb3.ClientConfig{
		Host:         INFLUXDB_HOST,
		Token:        INFLUXDB_TOKEN,
		Organization: INFLUXDB_ORG,
		Database:     INFLUXDB_DATABASE,
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create InfluxDB3 client: %v", err))
	}
	defer influxClient.Close()
	log.Printf("InfluxDB3: host=%s org=%s db=%s", INFLUXDB_HOST, INFLUXDB_ORG, INFLUXDB_DATABASE)

	deviceMap := GetDevicesMap()
	log.Printf("Device registry: %d known devices", len(deviceMap))

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

		// Extract device ID from  device/DEVICE_ID/telemetry
		parts := strings.SplitN(topic, "/", 3)
		if len(parts) < 2 {
			log.Printf("[main] unexpected topic format: %s", topic)
			continue
		}
		deviceID := parts[1]

		// Gate on the device registry; drop messages from unknown devices.
		deviceModel, known := deviceMap[deviceID]
		if !known {
			log.Printf("[drop] unknown device_id=%s topic=%s", deviceID, topic)
			continue
		}

		log.Printf("[recv] topic=%s device_id=%s model=%s", topic, deviceID, deviceModel)
		parseMsg(ctx, influxClient, deviceID, deviceModel, payload)
	}
}
