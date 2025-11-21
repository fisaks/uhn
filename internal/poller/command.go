package poller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/uhn"
	"github.com/fisaks/uhn/internal/util"
)

func (p *busPollers) OnDeviceCommand(ctx context.Context, command uhn.IncomingDeviceCommand) error {
	poller, device := p.FindPollerAndDeviceByDeviceName(command.Device)
	if poller == nil || device == nil {
		return fmt.Errorf("device not found: %s", command.Device)
	}
	logging.Debug("Received device command", "device", command.Device, "action", command.Action, "address", command.Address, "value", command.Value, "pulseMs", command.PulseMs)
	ok := poller.PushCommand(uhn.DeviceCommand{
		ID:      command.ID,
		Device:  device,
		Action:  command.Action,
		Address: util.ToUint16(command.Address),
		Value:   util.ToUint16(command.Value),
		PulseMs: util.ToInt(command.PulseMs),
	})
	if !ok {
		return fmt.Errorf("command buffer full for device: %s", command.Device)
	}
	return nil
}
func (p *SerialBusPoller) PushCommand(cmd uhn.DeviceCommand) bool {
	if p.cmdCh == nil {
		return false
	}
	select {
	case p.cmdCh <- cmd:
		return true
	default:
		return false
	}

}
func (p *busPollers) OnCommand(ctx context.Context, command uhn.IncomingCommand) error {
	if(command.Action == "resync") {
		logging.Info("Received resync command")
		p.edgePublisher.ClearPublishedState()
	}
		
	return nil
}

func (p *SerialBusPoller) handleCommand(ctx context.Context, c uhn.DeviceCommand) {
	// Resolve device -> unitId

	/*if err := p.ensureConnected(ctx); err != nil {
		PublishEvent(p.Publisher.Client, p.Publisher.TopicPrefix, c.Device.Name, "commandError",
			map[string]any{"reason": "connect", "error": err.Error()})
		return
	}*/

	switch strings.ToLower(c.Action) {
	case "setdigitaloutput":
		switch c.Value {
		case 0, 1:
			val := false
			if c.Value == 1 {
				val = true
			}
			p.scheduler.ClearPulse(c)
			p.client.WriteSingleDigitalOutput(ctx, c.Device, c.Address, val)

			if c.PulseMs > 0 {
				pulseCmd := c // copy
				pulseCmd.PulseMs = 0
				if c.Value == 1 {
					pulseCmd.Value = 0
				} else {
					pulseCmd.Value = 1
				}
				p.scheduler.SchedulePulse(pulseCmd, time.Duration(c.PulseMs)*time.Millisecond)

			}

		case 2:
			p.scheduler.ClearPulse(c)
			p.client.ToggleSingleDigitalOutput(ctx, c.Device, c.Address)
			if c.PulseMs > 0 {
				pulseCmd := c // copy
				pulseCmd.PulseMs = 0

				p.scheduler.SchedulePulse(pulseCmd, time.Duration(c.PulseMs)*time.Millisecond)
			}

		}

	default:
		logging.Warn("Unknown command action", "action", c.Action)
		//PublishEvent(p.Publisher.Client, p.Publisher.TopicPrefix, c.Device.Name, "commandError",
		//	map[string]any{"reason": "unknown action", "action": c.Action})
		return
	}

}
