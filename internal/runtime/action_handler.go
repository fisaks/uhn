package runtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
	"github.com/fisaks/uhn/internal/poller"
	"github.com/fisaks/uhn/internal/uhn"
)

// EdgeActionHandler handles actions emitted by the edge rule runtime.
// It mirrors the master's rule-action.dispatcher.ts.
type EdgeActionHandler struct {
	edgeName      string
	pollers       poller.BusPollers
	broker        messaging.Broker
	ipcBridge     *IPCBridge
	signalTracker *SignalTracker
}

// NewEdgeActionHandler creates an action handler for edge rule execution.
func NewEdgeActionHandler(
	edgeName string,
	pollers poller.BusPollers,
	broker messaging.Broker,
	ipcBridge *IPCBridge,
	signalTracker *SignalTracker,
) *EdgeActionHandler {
	return &EdgeActionHandler{
		edgeName:      edgeName,
		pollers:       pollers,
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
	case "mute", "clearMute":
		h.handleMuteAction(ctx, action)
	default:
		logging.Warn("Unknown action type", "type", action.Type, "resourceId", action.ResourceID)
	}
}

// handleSetDigitalOutput pushes a digital output command to the appropriate poller.
func (h *EdgeActionHandler) handleSetDigitalOutput(ctx context.Context, action RuntimeAction, resource *RuntimeResource) {
	if resource.Type != "digitalOutput" {
		logging.Warn("setDigitalOutput on non-digitalOutput resource", "resourceId", action.ResourceID, "type", resource.Type)
		return
	}

	if resource.Pin == nil {
		logging.Warn("setDigitalOutput on resource without pin", "resourceId", action.ResourceID)
		return
	}

	bp, device := h.pollers.FindPollerAndDeviceByDeviceName(resource.Device)
	if bp == nil || device == nil {
		logging.Warn("setDigitalOutput: device not found", "resourceId", action.ResourceID, "device", resource.Device)
		return
	}

	value := boolToUint16(action.Value)
	cmd := uhn.DeviceCommand{
		Device:  device,
		Action:  "setdigitaloutput",
		Address: uint16(*resource.Pin),
		Value:   value,
	}

	if !bp.PushCommand(cmd) {
		logging.Warn("setDigitalOutput: command buffer full", "resourceId", action.ResourceID, "device", resource.Device)
	} else {
		logging.Debug("setDigitalOutput pushed", "resourceId", action.ResourceID, "pin", *resource.Pin, "value", value)
	}
}

// handleSetAnalogOutput pushes an analog output command to the appropriate poller.
func (h *EdgeActionHandler) handleSetAnalogOutput(ctx context.Context, action RuntimeAction, resource *RuntimeResource) {
	if resource.Type != "analogOutput" {
		logging.Warn("setAnalogOutput on non-analogOutput resource", "resourceId", action.ResourceID, "type", resource.Type)
		return
	}

	if resource.Pin == nil {
		logging.Warn("setAnalogOutput on resource without pin", "resourceId", action.ResourceID)
		return
	}

	bp, device := h.pollers.FindPollerAndDeviceByDeviceName(resource.Device)
	if bp == nil || device == nil {
		logging.Warn("setAnalogOutput: device not found", "resourceId", action.ResourceID, "device", resource.Device)
		return
	}

	value := toUint16Value(action.Value)
	cmd := uhn.DeviceCommand{
		Device:  device,
		Action:  "setanalogoutput",
		Address: uint16(*resource.Pin),
		Value:   value,
	}

	if !bp.PushCommand(cmd) {
		logging.Warn("setAnalogOutput: command buffer full", "resourceId", action.ResourceID, "device", resource.Device)
	} else {
		logging.Debug("setAnalogOutput pushed", "resourceId", action.ResourceID, "pin", *resource.Pin, "value", value)
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
func (h *EdgeActionHandler) handleEmitSignal(ctx context.Context, action RuntimeAction, resource *RuntimeResource) {
	timestamp := time.Now().UnixMilli()

	// Update local IPC bridge state so the runtime sees it immediately
	h.ipcBridge.HandleSignalUpdate(action.ResourceID, action.Value, timestamp)

	// Publish to MQTT so master receives the signal
	topic := "signal/state/" + action.ResourceID
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
