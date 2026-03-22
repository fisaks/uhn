package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
)

// DevicePinStatePublisher publishes per-pin physical state to MQTT.
// Used by drivers that produce individual typed values (IHC, Zigbee, etc.)
// as opposed to device-level byte arrays (Modbus).
//
// Topic: device/{device}/pin/{pin} (broker auto-prefixes with uhn/{edge}/).
// Physical state is hardware-level and independent of blueprint resource names.
// For numeric pins the topic segment is hex (e.g. "0x9F085E").
// For string pins (Z2M property names) the segment is the literal string.
type DevicePinStatePublisher struct {
	broker messaging.Broker
}

// DevicePinStatePayload is the MQTT payload for per-pin physical state.
// The device name and pin are also in the topic path for subscription routing.
// The payload pin can be a number (IHC, Modbus) or string (Z2M property name).
type DevicePinStatePayload struct {
	Type      string `json:"type"`      // "digitalOutput", "digitalInput", "analogOutput", "analogInput"
	Pin       any    `json:"pin"`       // physical pin / resource ID (int) or Z2M property name (string)
	Value     any    `json:"value"`     // typed value: bool for digital, int/float for analog
	Timestamp int64  `json:"timestamp"` // unix millis
}

// NewDevicePinStatePublisher creates a new DevicePinStatePublisher.
func NewDevicePinStatePublisher(broker messaging.Broker) *DevicePinStatePublisher {
	return &DevicePinStatePublisher{broker: broker}
}

// Publish publishes a single pin's physical state to MQTT.
func (p *DevicePinStatePublisher) Publish(ctx context.Context, device, resourceType string, pin any, value any, timestamp int64) {
	topic := fmt.Sprintf("device/%s/pin/%s", device, formatPinForTopic(pin))
	payload := DevicePinStatePayload{
		Type:      resourceType,
		Pin:       pin,
		Value:     value,
		Timestamp: timestamp,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		logging.Error("DevicePinStatePublisher: marshal failed", "device", device, "pin", config.FormatPin(pin), "error", err)
		return
	}

	if err := p.broker.Publish(ctx, topic, messaging.AtLeastOnce, true, data); err != nil {
		logging.Error("DevicePinStatePublisher: MQTT publish failed", "device", device, "pin", config.FormatPin(pin), "error", err)
	} else {
		logging.Debug("DevicePinStatePublisher: published", "device", device, "pin", config.FormatPin(pin), "type", resourceType, "value", value)
	}
}

// formatPinForTopic formats a pin for use in MQTT topic segments.
// Numeric pins are hex-formatted (e.g. "0x9F085E"). String pins are literal.
func formatPinForTopic(pin any) string {
	switch v := pin.(type) {
	case float64:
		return fmt.Sprintf("0x%X", int(v))
	case int:
		return fmt.Sprintf("0x%X", v)
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}
