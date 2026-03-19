package uhn

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fisaks/uhn/internal/config"
)

type DeviceState struct {
	Timestamp      time.Time `json:"timestamp"`
	TimestampMs    int64     `json:"timestampMs"`
	Name           string    `json:"name"`
	DigitalOutputs []byte    `json:"digitalOutputs,omitempty"`
	DigitalInputs  []byte    `json:"digitalInputs,omitempty"`
	AnalogOutputs  []byte    `json:"analogOutputs,omitempty"`
	AnalogInputs   []byte    `json:"analogInputs,omitempty"`
	Status         string    `json:"status"` // "ok", "error", "partial_error"
	Errors         []string  `json:"errors,omitempty"`
}

type IncomingCommand struct {
	ID      string          `json:"id,omitempty"`
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type IncomingDeviceCommand struct {
	ID      string `json:"id,omitempty"`
	Device  string `json:"device,omitempty"` // overridden by topic
	Action  string `json:"action"`
	Address any    `json:"address,omitempty"` // accept number or string
	Value   any    `json:"value,omitempty"`   // 0=off,1=on,2=toggle or analog
	PulseMs any    `json:"pulseMs,omitempty"`
}
type DeviceCommand struct {
	ID      string
	Device  *config.DeviceConfig
	Action  string
	Address uint16
	Value   uint16
	PulseMs int
}

type CommandPusher interface {
	PushCommand(cmd DeviceCommand) bool
}
type EdgePublisher interface {
	PublishDeviceState(ctx context.Context, state DeviceState) error
	ClearPublishedState()
}
type EdgeSubscriber interface {
	OnDeviceCommand(ctx context.Context, command IncomingDeviceCommand) error
	OnCommand(ctx context.Context, command IncomingCommand) error
}

// StateUpdater is the interface drivers use to push physical state updates
// to the IPCBridge. This avoids a direct dependency on the runtime package.
type StateUpdater interface {
	UpdatePhysicalStateByAddress(ctx context.Context, device, resourceType string, pin int, value any, timestamp int64)
}

// DeviceDriver is the generic driver interface for protocol-agnostic device
// interaction. The action handler dispatches through this interface instead of
// branching on protocol type. IHC implements it; future Zigbee/Mi Light drivers
// will too.
type DeviceDriver interface {
	// HandleSignal forwards a signal to the physical device.
	HandleSignal(ctx context.Context, resourceID int, value any) error
	// SetOutput writes an output value to the physical device.
	SetOutput(ctx context.Context, resourceID int, value any) error
	// BypassSignalState returns true if signal overrides (S) should be skipped
	// for this driver. When true, emitSignal forwards to the device instead of
	// setting local signal state — the device's controller is the source of
	// truth and state flows back as physical state (P).
	BypassSignalState() bool
}
