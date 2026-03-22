package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/uhn"
	"github.com/fisaks/uhn/internal/util"
)

// DeviceCommandHandler intercepts device commands (device/{name}/cmd) and routes
// them to IHC drivers when applicable, falling back to the Modbus poller path.
//
// For IHC devices:
//   - setdigitaloutput: resolves toggle (value=2) using current computed state,
//     then calls driver.SetOutput(pin, bool)
//   - setanalogoutput: calls driver.SetOutput(pin, value) directly
//
// For non-IHC devices: delegates to the poller-based EdgeSubscriber.
type DeviceCommandHandler struct {
	drivers   map[string]uhn.DeviceDriver
	ipcBridge *IPCBridge
	delegate  uhn.EdgeSubscriber
}

// NewDeviceCommandHandler creates a handler that routes device commands to
// IHC drivers or falls back to the delegate (pollers).
func NewDeviceCommandHandler(drivers map[string]uhn.DeviceDriver, ipcBridge *IPCBridge, delegate uhn.EdgeSubscriber) *DeviceCommandHandler {
	return &DeviceCommandHandler{
		drivers:   drivers,
		ipcBridge: ipcBridge,
		delegate:  delegate,
	}
}

// OnDeviceCommand routes the command to an IHC driver or falls back to pollers.
func (h *DeviceCommandHandler) OnDeviceCommand(ctx context.Context, command uhn.IncomingDeviceCommand) error {
	driver, ok := h.drivers[command.Device]
	if !ok {
		return h.delegate.OnDeviceCommand(ctx, command)
	}

	action := strings.ToLower(command.Action)

	// Pass address as-is for driver devices (Z2M uses string pins like "state").
	// Non-driver devices (Modbus) go through the delegate which handles int conversion.
	address := command.Address

	switch action {
	case "setdigitaloutput":
		return h.handleDigitalOutput(ctx, command.Device, address, command.Value, driver)
	case "setanalogoutput":
		return h.handleAnalogOutput(ctx, command.Device, address, command.Value, driver)
	default:
		logging.Warn("DeviceCommandHandler: unknown action for driver device",
			"device", command.Device, "action", command.Action)
		return fmt.Errorf("unknown action: %s", command.Action)
	}
}

// OnCommand delegates to the pollers (no driver-specific handling needed).
func (h *DeviceCommandHandler) OnCommand(ctx context.Context, command uhn.IncomingCommand) error {
	return h.delegate.OnCommand(ctx, command)
}

func (h *DeviceCommandHandler) handleDigitalOutput(ctx context.Context, device string, pin any, rawValue any, driver uhn.DeviceDriver) error {
	value := util.ToUint16(rawValue)

	var boolValue bool
	switch value {
	case 0:
		boolValue = false
	case 1:
		boolValue = true
	case 2:
		// Toggle: resolve using current computed state
		boolValue = !h.getCurrentBoolState(device, "digitalOutput", pin)
		logging.Debug("DeviceCommandHandler: resolved toggle",
			"device", device, "pin", config.FormatPin(pin), "resolved", boolValue)
	default:
		return fmt.Errorf("invalid digital output value: %d", value)
	}

	logging.Debug("DeviceCommandHandler: setDigitalOutput via driver",
		"device", device, "pin", config.FormatPin(pin), "value", boolValue)
	return driver.SetOutput(ctx, pin, boolValue)
}

func (h *DeviceCommandHandler) handleAnalogOutput(ctx context.Context, device string, pin any, rawValue any, driver uhn.DeviceDriver) error {
	logging.Debug("DeviceCommandHandler: setAnalogOutput via driver",
		"device", device, "pin", config.FormatPin(pin), "value", rawValue)
	return driver.SetOutput(ctx, pin, rawValue)
}

// getCurrentBoolState reads the current computed state for a resource by physical address.
// Returns false if the state is unknown or not boolean.
func (h *DeviceCommandHandler) getCurrentBoolState(device, resourceType string, pin any) bool {
	rm := h.ipcBridge.getResourceMap()
	if rm == nil {
		return false
	}
	resourceID, ok := rm.LookupResourceID(device, resourceType, pin)
	if !ok {
		return false
	}

	h.ipcBridge.stateMu.RLock()
	defer h.ipcBridge.stateMu.RUnlock()
	val, exists := h.ipcBridge.computedState[resourceID]
	if !exists {
		return false
	}
	b, ok := val.(bool)
	if !ok {
		return false
	}
	return b
}
