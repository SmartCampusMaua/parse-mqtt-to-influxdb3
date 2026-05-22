// Package chirpstack parses Chirpstack v4 LoRaWAN uplink JSON messages.
// Returns a record.LNSFrame with the base64 payload, timestamp, and fPort.
package chirpstack

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/internal/record"
)

// message is the minimal Chirpstack v4 uplink structure (unknown fields ignored).
type message struct {
	FPort  uint64 `json:"fPort"`
	RxInfo []struct {
		NsTime time.Time `json:"nsTime"`
	} `json:"rxInfo"`
	Data string `json:"data"`
}

// Parse decodes a raw Chirpstack v4 uplink JSON string.
// Returns error when the message is malformed or contains no rxInfo.
func Parse(raw string) (*record.LNSFrame, error) {
	var msg message
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		return nil, fmt.Errorf("chirpstack: unmarshal: %w", err)
	}
	if len(msg.RxInfo) == 0 {
		return nil, fmt.Errorf("chirpstack: no rxInfo in message")
	}
	return &record.LNSFrame{
		Data:      msg.Data,
		Timestamp: msg.RxInfo[0].NsTime,
		Port:      msg.FPort,
	}, nil
}
