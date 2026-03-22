package zigbee

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fisaks/uhn/internal/logging"

	"github.com/fisaks/uhn/internal/messaging"
)

// ZigbeeDriver implements uhn.DeviceDriver for a single Z2M device.
// It sends /set commands to Z2M for writable properties.
type ZigbeeDriver struct {
	broker             messaging.Broker
	baseTopic          string
	deviceName         string
	writableProperties map[string]bool
}

// NewZigbeeDriver creates a driver for one Z2M device.
func NewZigbeeDriver(
	broker messaging.Broker,
	baseTopic string,
	deviceName string,
	writableProperties map[string]bool,
) *ZigbeeDriver {
	return &ZigbeeDriver{
		broker:             broker,
		baseTopic:          baseTopic,
		deviceName:         deviceName,
		writableProperties: writableProperties,
	}
}

// SetOutput publishes a value to {baseTopic}/{deviceName}/set.
// Pin is the Z2M property name (string).
func (d *ZigbeeDriver) SetOutput(ctx context.Context, pin any, value any) error {
	propName, ok := pin.(string)
	if !ok {
		return fmt.Errorf("Zigbee %s: expected string pin, got %T", d.deviceName, pin)
	}

	if !d.writableProperties[propName] {
		return fmt.Errorf("Zigbee %s: property %q is not writable", d.deviceName, propName)
	}

	// Convert boolean values to ON/OFF for Z2M enum state properties
	sendValue := value
	if b, ok := value.(bool); ok {
		if b {
			sendValue = "ON"
		} else {
			sendValue = "OFF"
		}
	}

	// Build the set payload: {"property": value}
	payload := map[string]any{propName: sendValue}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("Zigbee %s: marshal set payload: %w", d.deviceName, err)
	}

	topic := fmt.Sprintf("%s/%s/set", d.baseTopic, d.deviceName)

	// Publish directly to Z2M topic namespace (no uhn/{edge}/ prefix)
	if err := d.broker.PublishRaw(ctx, topic, messaging.AtLeastOnce, false, data); err != nil {
		return fmt.Errorf("Zigbee %s: publish set: %w", d.deviceName, err)
	}

	logging.Debug("Zigbee: sent /set command",
		"device", d.deviceName, "property", propName, "value", value)
	return nil
}

// HandleSignal is not used for Z2M devices (BypassSignalState = false).
// Signals are handled via local signal state (S), not forwarded to the driver.
func (d *ZigbeeDriver) HandleSignal(ctx context.Context, pin any, value any) error {
	logging.Error("Zigbee: HandleSignal called unexpectedly (BypassSignalState is false)",
		"device", d.deviceName, "pin", pin, "value", value)
	return fmt.Errorf("Zigbee %s: HandleSignal not supported (BypassSignalState is false)", d.deviceName)
}

// BypassSignalState returns false — Z2M state flows back through MQTT
// notifications, so the standard S/P model applies.
func (d *ZigbeeDriver) BypassSignalState() bool { return false }
