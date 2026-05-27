package kron

import (
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/go-parse/record"
)

// ParseKS3000WiFi parses KS3000 WiFi JSON payloads.
func ParseKS3000WiFi(message, deviceID, deviceModel string) []record.SensorDataRecord {
	return parseKS3000WiFi(message, deviceID, deviceModel)
}

func parseKS3000WiFi(message, deviceID, deviceModel string) []record.SensorDataRecord {
	var msgs []Message
	if err := json.Unmarshal([]byte(message), &msgs); err != nil || len(msgs) == 0 {
		log.Printf("[kron] ParseKS3000WiFi[%s]: %v", deviceID, err)
		return nil
	}
	entry := msgs[0]
	t, err := time.Parse("2006-01-02 15:04:05", entry.Time)
	if err != nil {
		log.Printf("[kron] ParseKS3000WiFi[%s]: timestamp error (%v), using now", deviceID, err)
		t = time.Now().UTC()
	}
	return buildRecords(entry.Metadata, strings.ToLower(deviceModel), deviceID, "custom", t)
}
