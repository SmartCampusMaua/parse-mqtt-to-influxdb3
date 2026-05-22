// Package everynet parses Everynet LoRaWAN uplink JSON messages.
// Returns a record.LNSFrame with the base64 payload, timestamp, and fPort.
package everynet

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/OpenDataTelemetry/device-gateway-mqtt/internal/record"
)

// message is the minimal Everynet uplink structure (unknown fields ignored).
type message struct {
	Type   string `json:"type"`
	Params struct {
		Payload string  `json:"payload"`
		RxTime  float64 `json:"rx_time"` // Unix seconds, fractional
		Port    uint64  `json:"port"`
	} `json:"params"`
}

// Parse decodes a raw Everynet uplink JSON string.
// Everynet payloads may contain extra whitespace that is stripped first.
// Returns error for non-uplink frames (e.g. downlink ACK, join).
func Parse(raw string) (*record.LNSFrame, error) {
	s := strings.ReplaceAll(raw, " ", "")
	var msg message
	if err := json.Unmarshal([]byte(s), &msg); err != nil {
		return nil, fmt.Errorf("everynet: unmarshal: %w", err)
	}
	if msg.Type != "uplink" {
		return nil, fmt.Errorf("everynet: not an uplink (type=%q)", msg.Type)
	}
	// Split float seconds into integer seconds + sub-second nanoseconds
	// to preserve fractional precision without float64 multiplication overflow.
	var ts time.Time
	if msg.Params.RxTime > 0 {
		sec  := int64(msg.Params.RxTime)
		nsec := int64(math.Round((msg.Params.RxTime - float64(sec)) * 1e9))
		ts = time.Unix(sec, nsec).UTC()
	} else {
		ts = time.Now().UTC()
	}
	return &record.LNSFrame{
		Data:      msg.Params.Payload,
		Timestamp: ts,
		Port:      msg.Params.Port,
	}, nil
}
