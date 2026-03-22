package milight

import (
	"context"
	"fmt"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/util"
)

// Pin constants for the Mi-Light FUT069 resource mapping.
// Pins 0-1 = digitalOutput (power, night mode), pins 2-4 = digitalInput push (buttons),
// pins 5-9 = analogOutput.
const (
	PinPower     = 0 // digitalOutput: on/off
	PinNightMode = 1 // digitalOutput: night mode on/off (driver handles exit via power cycle)
	PinWhiteMode = 2 // digitalInput push: white/CCT mode button
	PinSpeedUp   = 3 // digitalInput push: effect speed up button
	PinSpeedDown = 4 // digitalInput push: effect speed down button

	PinBrightness = 5 // analogOutput: 0-100
	PinColorTemp  = 6 // analogOutput: 0-100 (0=warm, 100=cool)
	PinHue        = 7 // analogOutput: 0-255
	PinSaturation = 8 // analogOutput: 0-100 (inverted: 0=white, 100=vivid)
	PinMode       = 9 // analogOutput: 1-9
)

// MilightDriver manages a single Mi-Light zone. Implements DeviceDriver.
type MilightDriver struct {
	transport *MilightTransport
	zone      byte
	device    string
}

// NewMilightDriver creates a driver for one zone on a transport.
func NewMilightDriver(transport *MilightTransport, zoneCfg *config.MilightZoneConfig) *MilightDriver {
	return &MilightDriver{
		transport: transport,
		zone:      zoneCfg.Zone,
		device:    zoneCfg.Name,
	}
}

// SetOutput writes an output value to the Mi-Light zone.
func (d *MilightDriver) SetOutput(ctx context.Context, pin any, value any) error {
	pinInt, ok := toInt(pin)
	if !ok {
		return fmt.Errorf("Mi-Light %s: expected numeric pin, got %T", d.device, pin)
	}
	cmds, err := d.buildCommands(pinInt, value)
	if err != nil {
		return err
	}
	for _, cmd := range cmds {
		cmd.zone = d.zone
		cmd.device = d.device
		if err := d.transport.enqueue(cmd); err != nil {
			return err
		}
	}
	return nil
}

// HandleSignal forwards a signal to the device — delegates to SetOutput.
func (d *MilightDriver) HandleSignal(ctx context.Context, pin any, value any) error {
	return d.SetOutput(ctx, pin, value)
}

// BypassSignalState returns true — Mi-Light is one-way, so signals must go
// through the driver to publish assumed state. Without this, S would stick forever.
func (d *MilightDriver) BypassSignalState() bool { return true }

// buildCommands converts a pin + value into one or more transport commands.
// Most pins produce a single command; night mode toggle-off produces two (off + on).
func (d *MilightDriver) buildCommands(pin int, value any) ([]transportCommand, error) {
	switch pin {
	// ── Digital output (power) ───────────────────────────────────────────
	case PinPower:
		b, ok := toBool(value)
		if !ok {
			return nil, fmt.Errorf("Mi-Light %s pin %d: expected bool, got %T", d.device, pin, value)
		}
		cmd := transportCommand{resType: "digitalOutput", pin: PinPower, value: b}
		if b {
			cmd.cmd = CmdPowerOn()
		} else {
			cmd.cmd = CmdPowerOff()
			// Power off exits night mode on the physical light — reset assumed state
			cmd.sideEffects = []sideEffect{{resType: "digitalOutput", pin: PinNightMode, value: false}}
		}
		return []transportCommand{cmd}, nil

	// ── Night mode (on/off) ─────────────────────────────────────────────
	case PinNightMode:
		b, ok := toBool(value)
		if !ok {
			return nil, fmt.Errorf("Mi-Light %s pin %d: expected bool, got %T", d.device, pin, value)
		}
		if b {
			// Enter night mode
			return []transportCommand{{
				cmd: CmdNightMode(), resType: "digitalOutput", pin: PinNightMode, value: true,
			}}, nil
		}
		// Exit night mode: power off → power on (two commands)
		return []transportCommand{
			{
				cmd: CmdPowerOff(), resType: "digitalOutput", pin: PinPower, value: false,
				sideEffects: []sideEffect{{resType: "digitalOutput", pin: PinNightMode, value: false}},
			},
			{
				cmd: CmdPowerOn(), resType: "digitalOutput", pin: PinPower, value: true,
			},
		}, nil

	// ── Digital input push buttons ──────────────────────────────────────
	case PinWhiteMode:
		return []transportCommand{{
			cmd: CmdColorTemp(0x64), resType: "digitalInput", pin: PinWhiteMode, value: true,
		}}, nil

	case PinSpeedUp:
		return []transportCommand{{
			cmd: CmdModeSpeedUp(), resType: "digitalInput", pin: PinSpeedUp, value: true,
		}}, nil

	case PinSpeedDown:
		return []transportCommand{{
			cmd: CmdModeSpeedDown(), resType: "digitalInput", pin: PinSpeedDown, value: true,
		}}, nil

	// ── Analog outputs ───────────────────────────────────────────────────
	case PinBrightness:
		v, ok := toInt(value)
		if !ok {
			return nil, fmt.Errorf("Mi-Light %s pin %d: expected number, got %T", d.device, pin, value)
		}
		v = util.Clamp(v, 0, 100)
		return []transportCommand{{
			cmd: CmdBrightness(byte(v)), resType: "analogOutput", pin: PinBrightness, value: v,
		}}, nil

	case PinColorTemp:
		v, ok := toInt(value)
		if !ok {
			return nil, fmt.Errorf("Mi-Light %s pin %d: expected number, got %T", d.device, pin, value)
		}
		v = util.Clamp(v, 0, 100)
		return []transportCommand{{
			cmd: CmdColorTemp(byte(v)), resType: "analogOutput", pin: PinColorTemp, value: v,
		}}, nil

	case PinHue:
		v, ok := toInt(value)
		if !ok {
			return nil, fmt.Errorf("Mi-Light %s pin %d: expected number, got %T", d.device, pin, value)
		}
		v = util.Clamp(v, 0, 255)
		return []transportCommand{{
			cmd: CmdHue(byte(v)), resType: "analogOutput", pin: PinHue, value: v,
		}}, nil

	case PinSaturation:
		v, ok := toInt(value)
		if !ok {
			return nil, fmt.Errorf("Mi-Light %s pin %d: expected number, got %T", d.device, pin, value)
		}
		v = util.Clamp(v, 0, 100)
		// Invert: user 0=white, 100=vivid → protocol 0=vivid, 100=white
		return []transportCommand{{
			cmd: CmdSaturation(byte(100 - v)), resType: "analogOutput", pin: PinSaturation, value: v,
		}}, nil

	case PinMode:
		v, ok := toInt(value)
		if !ok {
			return nil, fmt.Errorf("Mi-Light %s pin %d: expected number, got %T", d.device, pin, value)
		}
		v = util.Clamp(v, 1, 9)
		return []transportCommand{{
			cmd: CmdMode(byte(v)), resType: "analogOutput", pin: PinMode, value: v,
		}}, nil

	default:
		return nil, fmt.Errorf("Mi-Light %s: unknown pin %d", d.device, pin)
	}
}

// toBool converts a value to bool.
func toBool(v any) (bool, bool) {
	switch val := v.(type) {
	case bool:
		return val, true
	case float64:
		return val != 0, true
	case int:
		return val != 0, true
	default:
		return false, false
	}
}

// toInt converts a value to int.
func toInt(v any) (int, bool) {
	switch val := v.(type) {
	case float64:
		return int(val), true
	case int:
		return val, true
	case int64:
		return int(val), true
	default:
		return 0, false
	}
}
