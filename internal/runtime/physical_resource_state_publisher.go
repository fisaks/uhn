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
// Used by drivers that produce individual typed values (IHC, future Zigbee, etc.)
// as opposed to device-level byte arrays (Modbus).
//
// Topic: device/{device}/pin/{pin} (broker auto-prefixes with uhn/{edge}/).
// Physical state is hardware-level and independent of blueprint resource names.
type DevicePinStatePublisher struct {
	broker messaging.Broker
}

// DevicePinStatePayload is the MQTT payload for per-pin physical state.
// The device name and pin are also in the topic path for subscription routing.
// Currently the topic pin is always formatted as hex (e.g. "0x9F085E") which
// suits IHC; future drivers (Modbus, Zigbee) may need a configurable format.
// The payload pin is always an integer for machine processing.
type DevicePinStatePayload struct {
	Type      string `json:"type"`      // "digitalOutput", "digitalInput", "analogOutput", "analogInput"
	Pin       int    `json:"pin"`       // physical pin / resource ID (e.g. IHC resource ID)
	Value     any    `json:"value"`     // typed value: bool for digital, int/float for analog
	Timestamp int64  `json:"timestamp"` // unix millis
}

// NewDevicePinStatePublisher creates a new DevicePinStatePublisher.
func NewDevicePinStatePublisher(broker messaging.Broker) *DevicePinStatePublisher {
	return &DevicePinStatePublisher{broker: broker}
}

// Publish publishes a single pin's physical state to MQTT.
func (p *DevicePinStatePublisher) Publish(ctx context.Context, device, resourceType string, pin int, value any, timestamp int64) {
	topic := fmt.Sprintf("device/%s/pin/0x%X", device, pin)
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
