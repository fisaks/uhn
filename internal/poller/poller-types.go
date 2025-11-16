package poller

import (
	"context"

	"github.com/fisaks/uhn/internal/config"
)

type ZeroSignal struct{}

// Zero is the canonical value to send on signal channels.
var Zero ZeroSignal

type DeviceClient interface {
	EnsureConnected(ctx context.Context) error
	Close()

	ReadSingleDigitalOutput(ctx context.Context, device *config.DeviceConfig, addr uint16) (bool, error)
	WriteSingleDigitalOutput(ctx context.Context, device *config.DeviceConfig, addr uint16, value bool) error
	ToggleSingleDigitalOutput(ctx context.Context, device *config.DeviceConfig, addr uint16) (error)

	ReadSingleDigitalInput(ctx context.Context, device *config.DeviceConfig, addr uint16) (bool, error)

	ReadDeviceDigitalOutput(ctx context.Context, device *config.DeviceConfig) ([]byte, error)
	ReadDeviceDigitalInput(ctx context.Context, device *config.DeviceConfig) ([]byte, error)

	ReadDeviceAnalogOutput(ctx context.Context, device *config.DeviceConfig) ([]byte, error)
	ReadDeviceAnalogInput(ctx context.Context, device *config.DeviceConfig) ([]byte, error)
}

