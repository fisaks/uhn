package uhn

import (
	"context"
	"time"

	"github.com/fisaks/uhn/internal/config"
)

type DeviceState struct {
	Timestamp      time.Time `json:"timestamp"`
	Name           string    `json:"name"`
	DigitalOutputs []byte    `json:"digitalOutputs,omitempty"`
	DigitalInputs  []byte    `json:"digitalInputs,omitempty"`
	AnalogOutputs  []byte    `json:"analogOutputs,omitempty"`
	AnalogInputs   []byte    `json:"analogInputs,omitempty"`
	Status         string    `json:"status"` // "ok", "error", "partial_error"
	Errors         []string  `json:"errors,omitempty"`
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
}
type EdgeSubscriber interface {
	OnDeviceCommand(ctx context.Context, command IncomingDeviceCommand) error
}
