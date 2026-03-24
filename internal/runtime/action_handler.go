package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
	"github.com/fisaks/uhn/internal/poller"
	"github.com/fisaks/uhn/internal/uhn"
	"github.com/fisaks/uhn/internal/util"
)

// EdgeActionHandler handles actions emitted by the edge rule runtime.
// It mirrors the master's rule-action.dispatcher.ts.
type EdgeActionHandler struct {
	edgeName      string
	pollers       poller.BusPollers
	drivers       map[string]uhn.DeviceDriver // keyed by device name (IHC, future Zigbee/Mi Light)
	broker        messaging.Broker
	ipcBridge     *IPCBridge
	signalTracker *SignalTracker
}

// NewEdgeActionHandler creates an action handler for edge rule execution.
func NewEdgeActionHandler(
	edgeName string,
	pollers poller.BusPollers,
	drivers map[string]uhn.DeviceDriver,
	broker messaging.Broker,
	ipcBridge *IPCBridge,
	signalTracker *SignalTracker,
) *EdgeActionHandler {
	if drivers == nil {
		drivers = make(map[string]uhn.DeviceDriver)
	}
	return &EdgeActionHandler{
		edgeName:      edgeName,
		pollers:       pollers,
		drivers:       drivers,
		broker:        broker,
		ipcBridge:     ipcBridge,
		signalTracker: signalTracker,
	}
}

// HandleRuntimeAction dispatches a single action from the rule runtime.
func (h *EdgeActionHandler) HandleRuntimeAction(ctx context.Context, action RuntimeAction, resource *RuntimeResource) {

	if resource == nil && action.ResourceID != "" {
		logging.Warn("Action for unknown resource", "type", action.Type, "resourceId", action.ResourceID)
		return
	}

	switch action.Type {
	case "setDigitalOutput":
		h.handleSetDigitalOutput(ctx, action, resource)
	case "setAnalogOutput":
		h.handleSetAnalogOutput(ctx, action, resource)
	case "emitSignal":
		h.handleEmitSignal(ctx, action, resource)
	case "timerStart", "timerClear":
		// Timer actions are only emitted in master mode; they shouldn't arrive
		// on the edge action handler. Log a warning if they do.
		logging.Warn("Timer action received on edge action handler (unexpected)", "type", action.Type, "resourceId", action.ResourceID)
	case "emitAction":
		h.handleEmitAction(ctx, action, resource)
	case "mute", "clearMute":
		h.handleMuteAction(ctx, action)
	default:
		logging.Warn("Unknown action type", "type", action.Type, "resourceId", action.ResourceID)
	}
}

// handleSetDigitalOutput pushes a digital output command to the appropriate driver or poller.
func (h *EdgeActionHandler) handleSetDigitalOutput(ctx context.Context, action RuntimeAction, resource *RuntimeResource) {
	if resource.Type != "digitalOutput" {
		logging.Warn("setDigitalOutput on non-digitalOutput resource", "resourceId", action.ResourceID, "type", resource.Type)
		return
	}

	if resource.Pin == nil {
		logging.Warn("setDigitalOutput on resource without pin", "resourceId", action.ResourceID)
		return
	}

	// Try device driver first (IHC, Zigbee, Mi-Light)
	if driver, ok := h.drivers[resource.Device]; ok {
		if err := driver.SetOutput(ctx, resource.Pin, action.Value); err != nil {
			logging.Error("setDigitalOutput: driver error", "resourceId", action.ResourceID, "device", resource.Device, "error", err)
		} else {
			logging.Debug("setDigitalOutput via driver", "resourceId", action.ResourceID, "device", resource.Device, "pin", config.FormatPin(resource.Pin))
		}
		return
	}

	// Fall back to bus pollers (numeric pins only)
	bp, device := h.pollers.FindPollerAndDeviceByDeviceName(resource.Device)
	if bp == nil || device == nil {
		logging.Warn("setDigitalOutput: device not found", "resourceId", action.ResourceID, "device", resource.Device)
		return
	}

	pinInt := util.ToUint16(resource.Pin)
	value := boolToUint16(action.Value)
	cmd := uhn.DeviceCommand{
		Device:  device,
		Action:  "setdigitaloutput",
		Address: pinInt,
		Value:   value,
	}

	if !bp.PushCommand(cmd) {
		logging.Warn("setDigitalOutput: command buffer full", "resourceId", action.ResourceID, "device", resource.Device)
	} else {
		logging.Debug("setDigitalOutput pushed", "resourceId", action.ResourceID, "pin", config.FormatPin(pinInt), "value", value)
	}
}

// handleSetAnalogOutput pushes an analog output command to the appropriate driver or poller.
// For virtualAnalogOutput resources, updates local state directly via IPC bridge.
func (h *EdgeActionHandler) handleSetAnalogOutput(ctx context.Context, action RuntimeAction, resource *RuntimeResource) {
	if resource.Type == "virtualAnalogOutput" {
		h.ipcBridge.HandleSetState(ctx, action.ResourceID, action.Value, time.Now().UnixMilli())
		return
	}

	if resource.Type != "analogOutput" {
		logging.Warn("setAnalogOutput on non-analogOutput resource", "resourceId", action.ResourceID, "type", resource.Type)
		return
	}

	if resource.Pin == nil {
		logging.Warn("setAnalogOutput on resource without pin", "resourceId", action.ResourceID)
		return
	}

	// Try device driver first (IHC, Zigbee, Mi-Light)
	if driver, ok := h.drivers[resource.Device]; ok {
		if err := driver.SetOutput(ctx, resource.Pin, action.Value); err != nil {
			logging.Error("setAnalogOutput: driver error", "resourceId", action.ResourceID, "device", resource.Device, "error", err)
		} else {
			logging.Debug("setAnalogOutput via driver", "resourceId", action.ResourceID, "device", resource.Device, "pin", config.FormatPin(resource.Pin))
		}
		return
	}

	// Fall back to bus pollers (numeric pins only)
	bp, device := h.pollers.FindPollerAndDeviceByDeviceName(resource.Device)
	if bp == nil || device == nil {
		logging.Warn("setAnalogOutput: device not found", "resourceId", action.ResourceID, "device", resource.Device)
		return
	}

	pinInt := util.ToUint16(resource.Pin)
	value := toUint16Value(action.Value)
	cmd := uhn.DeviceCommand{
		Device:  device,
		Action:  "setanalogoutput",
		Address: pinInt,
		Value:   value,
	}

	if !bp.PushCommand(cmd) {
		logging.Warn("setAnalogOutput: command buffer full", "resourceId", action.ResourceID, "device", resource.Device)
	} else {
		logging.Debug("setAnalogOutput pushed", "resourceId", action.ResourceID, "pin", config.FormatPin(pinInt), "value", value)
	}
}

// toUint16Value converts a numeric value (typically float64 from JSON) to uint16.
func toUint16Value(v any) uint16 {
	switch val := v.(type) {
	case float64:
		return uint16(val)
	case int:
		return uint16(val)
	case int64:
		return uint16(val)
	default:
		return 0
	}
}

// handleEmitSignal updates local signal state and publishes to MQTT for master.
// For bypassSignalState drivers (IHC), the signal is forwarded to the controller
// and NO local signal state is updated — state flows back through the driver's
// notification mechanism as physical state (P).
func (h *EdgeActionHandler) handleEmitSignal(ctx context.Context, action RuntimeAction, resource *RuntimeResource) {
	// Check if a device driver handles this resource and bypasses signal state
	if resource != nil && resource.Pin != nil {
		if driver, ok := h.drivers[resource.Device]; ok && driver.BypassSignalState() {
			// Forward signal to the driver's controller only.
			// Do NOT update local signal state (S) or publish to MQTT signal topic.
			// The controller is the source of truth — state comes back through
			// the notification loop as physical state (P).
			// Setting S would mask P since C = S ?? P.
			if err := driver.HandleSignal(ctx, resource.Pin, action.Value); err != nil {
				logging.Error("emitSignal: driver error", "resourceId", action.ResourceID, "device", resource.Device, "error", err)
			} else {
				logging.Debug("emitSignal forwarded to driver", "resourceId", action.ResourceID, "device", resource.Device, "pin", config.FormatPin(resource.Pin))
			}
			return
		}
	}

	// Default path: update local signal state and publish to MQTT
	timestamp := time.Now().UnixMilli()

	// Update local IPC bridge state so the runtime sees it immediately
	h.ipcBridge.HandleSignalUpdate(action.ResourceID, action.Value, timestamp)

	// Publish to MQTT so master receives the signal
	topic := "resource/signal/" + action.ResourceID
	payload := SignalMQTTPayload{
		ResourceID: action.ResourceID,
		Value:      action.Value,
		Timestamp:  timestamp,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		logging.Error("emitSignal: marshal failed", "resourceId", action.ResourceID, "error", err)
		return
	}

	// Record in signal tracker before publishing (for self-echo prevention)
	h.signalTracker.RecordPublish(action.ResourceID, timestamp)

	if err := h.broker.Publish(ctx, topic, messaging.AtLeastOnce, true, data); err != nil {
		logging.Error("emitSignal: MQTT publish failed", "resourceId", action.ResourceID, "error", err)
	} else {
		logging.Debug("emitSignal published", "resourceId", action.ResourceID, "value", action.Value)
	}
}

// handleEmitAction validates depth, injects an actionEvent into the local runtime,
// and publishes to MQTT so the master can also process it.
//
// Publishes to device/{device}/action/{pin} (edge → master), NOT resource/cmd/
// (which is master → edge only). Master's ActionEventDispatcher picks this up.
func (h *EdgeActionHandler) handleEmitAction(ctx context.Context, action RuntimeAction, resource *RuntimeResource) {
	if resource == nil || resource.Type != "actionInput" {
		logging.Warn("emitAction on non-actionInput resource",
			"resourceId", action.ResourceID, "type", resourceTypeOrNil(resource))
		return
	}

	if action.Depth >= MaxActionDepth {
		logging.Warn("emitAction depth limit reached — dropping to prevent loop",
			"resourceId", action.ResourceID, "action", action.Action, "depth", action.Depth)
		return
	}

	if resource.Device == "" || resource.Pin == nil {
		logging.Warn("emitAction: resource missing device/pin", "resourceId", action.ResourceID)
		return
	}

	timestamp := time.Now().UnixMilli()

	// Inject actionEvent to local runtime
	cmd := ActionEventCommand{
		Kind: "event",
		Cmd:  "actionEvent",
		Payload: ActionEventPayload{
			ResourceID: action.ResourceID,
			Action:     action.Action,
			Metadata:   action.Metadata,
			Timestamp:  timestamp,
			Depth:      action.Depth,
		},
	}
	if err := h.ipcBridge.writeJSON(cmd); err != nil {
		logging.Error("emitAction: failed to forward to runtime", "resourceId", action.ResourceID, "error", err)
	}

	// Publish to MQTT so master runtime can also process it.
	// Uses device/{device}/action/{pin} (edge → master), same as physical Z2M action events.
	topic := fmt.Sprintf("device/%s/action/%v", resource.Device, resource.Pin)
	payload := map[string]any{
		"action":    action.Action,
		"timestamp": timestamp,
		"depth":     action.Depth,
	}
	if action.Metadata != nil {
		payload["metadata"] = action.Metadata
	}

	if err := h.broker.PublishJSON(ctx, topic, messaging.AtLeastOnce, false, payload); err != nil {
		logging.Error("emitAction: MQTT publish failed", "resourceId", action.ResourceID, "error", err)
	} else {
		logging.Debug("emitAction published", "resourceId", action.ResourceID, "action", action.Action, "depth", action.Depth)
	}
}

func resourceTypeOrNil(r *RuntimeResource) string {
	if r == nil {
		return "<nil>"
	}
	return r.Type
}

// handleMuteAction forwards mute actions to the local runtime via IPC (fast path)
// and publishes to MQTT so master can relay to all other runtimes.
func (h *EdgeActionHandler) handleMuteAction(ctx context.Context, action RuntimeAction) {
	// Forward to local runtime via IPC (the runtime already applied it locally,
	// but re-applying is idempotent and harmless)
	muteCmd := MuteCommand{
		Kind: "event",
		Cmd:  "muteCommand",
		Payload: MuteCommandPayload{
			TargetType: action.TargetType,
			TargetID:   action.TargetID,
			Action:     action.Type,
			ExpiresAt:  action.ExpiresAt,
			Identifier: action.Identifier,
		},
	}
	if err := h.ipcBridge.writeJSON(muteCmd); err != nil {
		logging.Error("mute: failed to forward to runtime", "error", err)
	}

	// Publish to MQTT so master receives and relays
	topic := "mute/event"
	payload := MuteMQTTPayload{
		TargetType: action.TargetType,
		TargetID:   action.TargetID,
		Action:     action.Type,
		ExpiresAt:  action.ExpiresAt,
		Identifier: action.Identifier,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		logging.Error("mute: marshal failed", "error", err)
		return
	}

	if err := h.broker.Publish(ctx, topic, messaging.FireAndForget, false, data); err != nil {
		logging.Error("mute: MQTT publish failed", "error", err)
	} else {
		logging.Debug("mute: published to MQTT", "targetType", action.TargetType, "targetId", action.TargetID, "action", action.Type)
	}
}

// SignalMQTTPayload is the JSON payload for signal state MQTT messages.
type SignalMQTTPayload struct {
	ResourceID string `json:"resourceId"`
	Value      any    `json:"value"`
	Timestamp  int64  `json:"timestamp"`
}

// boolToUint16 converts a boolean-ish value to 0 or 1.
func boolToUint16(v any) uint16 {
	switch val := v.(type) {
	case bool:
		if val {
			return 1
		}
		return 0
	case float64:
		if val != 0 {
			return 1
		}
		return 0
	default:
		return 0
	}
}

// Ensure EdgeActionHandler satisfies ActionHandler.
var _ ActionHandler = (*EdgeActionHandler)(nil)
