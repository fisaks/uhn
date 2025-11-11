package modbus

import (
	"errors"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/util"
)

// IncomingDeviceCommand is the loose JSON shape received from MQTT for device commands.
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

var ErrDeviceNotFound = errors.New("device not found")

func ResolveIncoming(buses map[string]*BusPoller, payload IncomingDeviceCommand, topicDevice string) (*BusPoller, *DeviceCommand, error) {
	// Trust topic device
	deviceName := topicDevice
	owner, device := findOwnerAndDevice(buses, deviceName)
	if owner == nil || device == nil {
		return nil, nil, ErrDeviceNotFound
	}

	command := &DeviceCommand{
		ID:      payload.ID,
		Device:  device,
		Action:  payload.Action,
		Address: util.ToUint16(payload.Address),
		Value:   util.ToUint16(payload.Value),
		PulseMs: util.ToInt(payload.PulseMs),
	}
	return owner, command, nil
}


func (p *BusPoller) EnqueueCommand(cmd DeviceCommand) bool {
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