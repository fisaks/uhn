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
	UpdatePhysicalStateByAddress(ctx context.Context, device, resourceType string, pin any, value any, timestamp int64)
}

// PhysicalStateReader provides read access to the latest physical state by device address.
// Used by transports that need to check gatekeeper state before sending commands.
type PhysicalStateReader interface {
	ReadPhysicalStateByAddress(device, resourceType string, pin any) (value any, ok bool)
}

// ResourceLookup checks if a physical resource exists in the current blueprint's ResourceMap.
// Used by Z2M transport to filter: only publish properties that are exported in the blueprint.
type ResourceLookup interface {
	// HasResourceMap returns true if a ResourceMap has been built (blueprint loaded).
	HasResourceMap() bool
	// HasResourceForAddress returns true if the given address exists in the ResourceMap.
	HasResourceForAddress(device, resourceType string, pin any) bool
	// GetDecimalPrecisionForAddress returns the decimal precision for a resource, or -1 if not set.
	GetDecimalPrecisionForAddress(device, resourceType string, pin any) int
}

// ResourceMapDeviceProvider extracts resources for a specific device from the
// current ResourceMap. Used by drivers (e.g. IHC) that need to subscribe to
// device-specific resources when the blueprint becomes available.
type ResourceMapDeviceProvider interface {
	// DeviceResourcesFromMap returns all resources for the given device.
	// Returns a map of {pinInt → resourceType} and true, or nil and false if
	// no ResourceMap is available yet.
	DeviceResourcesFromMap(device string) (map[int]string, bool)
}

// ActionEventEmitter forwards transient action events (e.g. Zigbee button presses) to the
// rule runtime. Unlike state updates, action events bypass the P/S/C state model entirely.
type ActionEventEmitter interface {
	EmitActionEvent(ctx context.Context, device string, pin string, action string, metadata map[string]any, timestamp int64)
}

// DeviceTransport manages the connection lifecycle for a hardware transport layer.
// One transport may serve multiple DeviceDrivers (e.g. Mi-Light iBox2 serves
// multiple zones, a Modbus serial bus serves multiple slave devices).
// For 1:1 topologies (e.g. IHC), the transport and driver may be backed by the
// same struct.
type DeviceTransport interface {
	// Start begins the transport lifecycle. Blocks until ctx is cancelled.
	Start(ctx context.Context)
	// Stop signals the transport to shut down and waits for completion.
	Stop()
}

// DeviceDriver is the generic driver interface for protocol-agnostic device
// interaction. The action handler dispatches through this interface instead of
// branching on protocol type.
type DeviceDriver interface {
	// HandleSignal forwards a signal to the physical device.
	HandleSignal(ctx context.Context, pin any, value any) error
	// SetOutput writes an output value to the physical device.
	SetOutput(ctx context.Context, pin any, value any) error
	// BypassSignalState returns true if signal overrides (S) should be skipped
	// for this driver. When true, emitSignal forwards to the device instead of
	// setting local signal state — the device's controller is the source of
	// truth and state flows back as physical state (P).
	BypassSignalState() bool
}
