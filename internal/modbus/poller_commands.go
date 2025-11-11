package modbus

import (
	"context"
	"strings"
	"time"

	"github.com/fisaks/uhn/internal/logging"
)

const ON uint16 = 0xFF00
const OFF uint16 = 0x0000
const TOGGLE uint16 = 0x5500

func (p *BusPoller) handleCommand(ctx context.Context, c DeviceCommand) {
	// Resolve device -> unitId

	if err := p.ensureConnected(ctx); err != nil {
		PublishEvent(p.Publisher.Client, p.Publisher.TopicPrefix, c.Device.Name, "commandError",
			map[string]any{"reason": "connect", "error": err.Error()})
		return
	}

	catalogDevice, _ := p.getDeviceSpec(*c.Device)

	switch strings.ToLower(c.Action) {
	case "setdigitaloutput":
		switch c.Value {
		case 0, 1:
			val := OFF
			if c.Value == 1 {
				val = ON
			}
			p.writeSingleDigitalOutput(ctx, *c.Device, c.Address, val)

			if c.PulseMs > 0 {
				device := c.Device // *config.DeviceConfig in your current code
				addr := c.Address
				invertVal := uint16(1 - int(c.Value)) // 1->0, 0->1
				delay := time.Duration(c.PulseMs) * time.Millisecond

				// build key for this device+address
				devName := device.Name
				key := timerKey(devName, addr)

				// cancel previous timer for same key (last pulse wins)
				p.timersMu.Lock()
				if old := p.timers[key]; old != nil {
					old.Stop() // best-effort
					delete(p.timers, key)
				}

				// schedule new timer
				t := time.AfterFunc(delay, func() {
					// cleanup entry
					p.timersMu.Lock()
					delete(p.timers, key)
					p.timersMu.Unlock()

					// build revert command (same action)
					revert := DeviceCommand{
						Device:  device,
						Action:  c.Action,
						Address: addr,
						Value:   invertVal,
					}

					// Enqueue safely. If enqueue fails, log/publish event.
					if ok := p.EnqueueCommand(revert); !ok {
						logging.Warn("pulse revert dropped (queue full or poller stopped)",
							"device", devName, "addr", addr)
						// optionally call PublishEvent(...) here to notify master
					}
				})

				p.timers[key] = t
				p.timersMu.Unlock()

			}

		case 2:
			if catalogDevice.Capabilities.ToggleWord != 0 {
				p.writeSingleDigitalOutput(ctx, *c.Device, c.Address, catalogDevice.Capabilities.ToggleWord)

				if c.PulseMs > 0 {
					time.Sleep(time.Duration(c.PulseMs) * time.Millisecond)
					p.writeSingleDigitalOutput(ctx, *c.Device, c.Address, catalogDevice.Capabilities.ToggleWord)
				}
			} else {
				currentValue, err := p.readSingleDigitalOutput(ctx, *c.Device, c.Address)
				if err != nil {
					PublishEvent(p.Publisher.Client, p.Publisher.TopicPrefix, c.Device.Name, "commandError",
						map[string]any{"reason": "toggleRead", "error": err.Error(), "address": c.Address})
					return
				}
				val := ON
				if currentValue {
					val = OFF
				}
				if err := p.writeSingleDigitalOutput(ctx, *c.Device, c.Address, val); err != nil {
					return
				}

			}
		}

	default:
		PublishEvent(p.Publisher.Client, p.Publisher.TopicPrefix, c.Device.Name, "commandError",
			map[string]any{"reason": "unknown action", "action": c.Action})
		return
	}

}
