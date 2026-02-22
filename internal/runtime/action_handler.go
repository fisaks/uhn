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
	if resource == nil {
		logging.Warn("Action for unknown resource", "type", action.Type, "resourceId", action.ResourceID)
		return
	}

	switch action.Type {
	case "setOutput":
		h.handleSetOutput(ctx, action, resource)
	case "emitSignal":
		h.handleEmitSignal(ctx, action, resource)
	case "timerStart", "timerClear":
		// Timer actions are only emitted in master mode; they shouldn't arrive
		// on the edge action handler. Log a warning if they do.
		logging.Warn("Timer action received on edge action handler (unexpected)", "type", action.Type, "resourceId", action.ResourceID)
	default:
		logging.Warn("Unknown action type", "type", action.Type, "resourceId", action.ResourceID)
	}
}

// handleSetOutput pushes a digital output command to the appropriate poller.
func (h *EdgeActionHandler) handleSetOutput(ctx context.Context, action RuntimeAction, resource *RuntimeResource) {
	if resource.Type != "digitalOutput" {
		logging.Warn("setOutput on non-output resource", "resourceId", action.ResourceID, "type", resource.Type)
		return
	}

	if resource.Pin == nil {
		logging.Warn("setOutput on resource without pin", "resourceId", action.ResourceID)
		return
	}

	bp, device := h.pollers.FindPollerAndDeviceByDeviceName(resource.Device)
	if bp == nil || device == nil {
		logging.Warn("setOutput: device not found", "resourceId", action.ResourceID, "device", resource.Device)
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
		logging.Warn("setOutput: command buffer full", "resourceId", action.ResourceID, "device", resource.Device)
	} else {
		logging.Debug("setOutput pushed", "resourceId", action.ResourceID, "pin", *resource.Pin, "value", value)
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
