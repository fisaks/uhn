package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/uhn"
)

const (
	// syntheticPressMs is the delay between synthetic activate and deactivate
	// injected into the runtime via IPC (in-process, no network).
	syntheticPressMs = 50
	// driverPulseMs is the delay between activate and deactivate sent to
	// a physical device driver over the network (e.g. IHC SOAP).
	driverPulseMs = 200
	// longPressBufferMs is added to the longPress thresholdMs so the runtime's
	// setTimeout safely fires before the synthetic release arrives.
	longPressBufferMs = 100
)

// ResourceCmdSubscriber implements messaging.Subscriber to handle incoming
// resource command MQTT messages on the topic "resource/cmd/+".
//
// For tap/longPress on digitalInput resources:
//   - Driver-owned (IHC): auto-pulse the driver → physical state round-trip
//     generates activated, deactivated, tap/longPress via InputGestureEmitter.
//   - No driver (Modbus): inject synthetic stateUpdate to runtime →
//     InputGestureEmitter auto-detects tap/longPress from state cycle.
//
// For tap on complex/virtualDigitalInput:
//   - Inject synthetic stateUpdate (activated/deactivated) + explicit tapCommand
//     (InputGestureEmitter doesn't handle these types).
type ResourceCmdSubscriber struct {
	ipcBridge     *IPCBridge
	signalTracker *SignalTracker
	drivers       map[string]uhn.DeviceDriver
}

// NewResourceCmdSubscriber creates a new resource command subscriber.
func NewResourceCmdSubscriber(ipcBridge *IPCBridge, signalTracker *SignalTracker) *ResourceCmdSubscriber {
	return &ResourceCmdSubscriber{
		ipcBridge:     ipcBridge,
		signalTracker: signalTracker,
	}
}

// SetDrivers sets the device drivers for auto-pulse on tap/longPress commands.
func (s *ResourceCmdSubscriber) SetDrivers(drivers map[string]uhn.DeviceDriver) {
	s.drivers = drivers
}

// OnMessage handles an incoming MQTT message on "resource/cmd/{resourceId}".
func (s *ResourceCmdSubscriber) OnMessage(ctx context.Context, topic string, payload []byte) {
	parts := strings.Split(topic, "/")
	if len(parts) != 5 {
		logging.Warn("ResourceCmdSubscriber: malformed topic", "topic", topic)
		return
	}
	resourceID := parts[4]

	var msg LogicalResourceCommandMQTTPayload
	if err := json.Unmarshal(payload, &msg); err != nil {
		logging.Warn("ResourceCmdSubscriber: invalid JSON", "topic", topic, "error", err)
		return
	}

	logging.Debug("ResourceCmdSubscriber: received command", "resourceId", resourceID, "action", msg.Action)

	switch msg.Action {
	case "tap":
		s.handleTap(ctx, resourceID, msg)
	case "longPress":
		s.handleLongPress(ctx, resourceID, msg)
	case "start", "clear":
		s.forwardTimerCommand(resourceID, msg)
	case "setState":
		s.ipcBridge.HandleSetState(ctx, msg.ResourceID, msg.Value, msg.Timestamp)
	default:
		logging.Warn("ResourceCmdSubscriber: unknown action", "resourceId", resourceID, "action", msg.Action)
	}
}

// handleTap processes a tap command with unified event generation.
func (s *ResourceCmdSubscriber) handleTap(ctx context.Context, resourceID string, msg LogicalResourceCommandMQTTPayload) {
	rm := s.ipcBridge.getResourceMap()
	resource := s.lookupResource(rm, resourceID)

	switch {
	case resource != nil && resource.Type == "digitalInput":
		// digitalInput: state cycle generates activated → deactivated → tap via InputGestureEmitter.
		// No explicit tapCommand needed.
		if driver, bypassSignal := s.getOwningDriver(resource); bypassSignal {
			// IHC: auto-pulse driver → physical state round-trip handles everything
			s.autoPulseDriver(ctx, resourceID, resource, driver)
		} else {
			// Modbus: inject synthetic state cycle
			s.injectSyntheticTap(resourceID, msg.Timestamp)
		}

	case resource != nil && (resource.Type == "complex" || resource.Type == "virtualDigitalInput"):
		// complex/virtualDigitalInput: InputGestureEmitter ignores these types,
		// so we need both synthetic state (for activated/deactivated) and explicit tapCommand.
		s.injectSyntheticTap(resourceID, msg.Timestamp)
		s.forwardTapCommand(resourceID, msg)

	default:
		// Fallback: forward tapCommand as-is (unknown resource or other type)
		s.forwardTapCommand(resourceID, msg)
	}
}

// handleLongPress processes a longPress command.
// When simulateHold is false (default), forwards longPressCommand directly to the
// runtime for instant rule execution. When true, simulates a physical hold so
// InputGestureEmitter detects the longPress from the state cycle.
func (s *ResourceCmdSubscriber) handleLongPress(ctx context.Context, resourceID string, msg LogicalResourceCommandMQTTPayload) {
	rm := s.ipcBridge.getResourceMap()
	resource := s.lookupResource(rm, resourceID)

	if resource != nil && resource.Type == "digitalInput" && msg.SimulateHold {
		// simulateHold: hold state for thresholdMs+buffer → InputGestureEmitter
		// detects longPress from the held state, then deactivated on release.
		holdMs := msg.DurationMs + longPressBufferMs
		if driver, bypassSignal := s.getOwningDriver(resource); bypassSignal {
			// bypassSignalState driver (IHC): hold driver signal on physical device
			s.holdDriverSignal(ctx, resourceID, resource, driver, holdMs)
		} else {
			// No bypassSignalState driver: inject synthetic hold into runtime
			s.injectSyntheticLongPress(resourceID, msg.Timestamp, holdMs)
		}
	} else {
		// Default (!simulateHold) or non-digitalInput: forward longPressCommand directly
		s.forwardLongPressCommand(resourceID, msg)
	}
}

// --- Driver helpers ---

func (s *ResourceCmdSubscriber) lookupResource(rm *ResourceMap, resourceID string) *RuntimeResource {
	if rm == nil {
		return nil
	}
	resource, ok := rm.LookupResource(resourceID)
	if !ok {
		return nil
	}
	return resource
}

func (s *ResourceCmdSubscriber) getOwningDriver(resource *RuntimeResource) (uhn.DeviceDriver, bool) {
	if s.drivers == nil || resource == nil || resource.Pin == nil {
		return nil, false
	}
	driver, ok := s.drivers[resource.Device]
	if !ok || !driver.BypassSignalState() {
		return nil, false
	}
	return driver, true
}

// autoPulseDriver sends HandleSignal(true) then HandleSignal(false) after
// driverPulseMs to simulate a momentary button press. The delay between
// activate and deactivate is necessary because IHC controllers need time to
// process the rising edge before receiving the falling edge — sending both
// back-to-back can cause the controller to double-toggle or miss the press.
// State flows back via the driver's notification mechanism (IHC SOAP notifications).
func (s *ResourceCmdSubscriber) autoPulseDriver(ctx context.Context, resourceID string, resource *RuntimeResource, driver uhn.DeviceDriver) {
	logging.Info("ResourceCmdSubscriber: auto-pulse driver",
		"resourceId", resourceID, "device", resource.Device, "pin", config.FormatPin(resource.Pin))

	if err := driver.HandleSignal(ctx, resource.Pin, true); err != nil {
		logging.Error("ResourceCmdSubscriber: auto-pulse true failed",
			"resourceId", resourceID, "error", err)
		return
	}

	pin := resource.Pin
	go func() {
		time.Sleep(driverPulseMs * time.Millisecond)
		if err := driver.HandleSignal(ctx, pin, false); err != nil {
			logging.Error("ResourceCmdSubscriber: auto-pulse false failed",
				"resourceId", resourceID, "error", err)
		}
	}()
}

// holdDriverSignal sends HandleSignal(true) and then HandleSignal(false) after holdMs.
// Used for longPress on IHC resources — the controller processes the sustained press.
func (s *ResourceCmdSubscriber) holdDriverSignal(ctx context.Context, resourceID string, resource *RuntimeResource, driver uhn.DeviceDriver, holdMs int64) {
	logging.Info("ResourceCmdSubscriber: hold driver signal",
		"resourceId", resourceID, "device", resource.Device,
		"pin", config.FormatPin(resource.Pin), "holdMs", holdMs)

	if err := driver.HandleSignal(ctx, resource.Pin, true); err != nil {
		logging.Error("ResourceCmdSubscriber: hold signal true failed",
			"resourceId", resourceID, "error", err)
		return
	}

	pin := resource.Pin
	go func() {
		time.Sleep(time.Duration(holdMs) * time.Millisecond)
		if err := driver.HandleSignal(ctx, pin, false); err != nil {
			logging.Error("ResourceCmdSubscriber: hold signal false failed",
				"resourceId", resourceID, "error", err)
		}
	}()
}

// --- Synthetic state helpers ---

// injectSyntheticTap injects stateUpdate(true) then stateUpdate(false) after syntheticPressMs
// to the runtime. InputGestureEmitter auto-detects tap from the state cycle for digitalInput;
// for other types the caller also sends an explicit tapCommand.
func (s *ResourceCmdSubscriber) injectSyntheticTap(resourceID string, timestamp int64) {
	logging.Debug("ResourceCmdSubscriber: injecting synthetic tap", "resourceId", resourceID)

	now := time.Now().UnixMilli()
	s.ipcBridge.InjectSyntheticState(resourceID, true, now)

	go func() {
		time.Sleep(syntheticPressMs * time.Millisecond)
		s.ipcBridge.InjectSyntheticState(resourceID, false, time.Now().UnixMilli())
	}()
}

// injectSyntheticLongPress injects stateUpdate(true), waits holdMs, then injects stateUpdate(false).
// InputGestureEmitter detects the longPress threshold during the hold.
func (s *ResourceCmdSubscriber) injectSyntheticLongPress(resourceID string, timestamp int64, holdMs int64) {
	logging.Debug("ResourceCmdSubscriber: injecting synthetic longPress",
		"resourceId", resourceID, "holdMs", holdMs)

	now := time.Now().UnixMilli()
	s.ipcBridge.InjectSyntheticState(resourceID, true, now)

	go func() {
		time.Sleep(time.Duration(holdMs) * time.Millisecond)
		s.ipcBridge.InjectSyntheticState(resourceID, false, time.Now().UnixMilli())
	}()
}

// --- IPC forwarding helpers ---

func (s *ResourceCmdSubscriber) forwardTapCommand(resourceID string, msg LogicalResourceCommandMQTTPayload) {
	cmd := TapCommand{
		Kind: "event",
		Cmd:  "tapCommand",
		Payload: TapCommandPayload{
			ResourceID: msg.ResourceID,
			Timestamp:  msg.Timestamp,
		},
	}
	if err := s.ipcBridge.writeJSON(cmd); err != nil {
		logging.Error("ResourceCmdSubscriber: failed to forward tap to runtime", "resourceId", resourceID, "error", err)
	}
}

func (s *ResourceCmdSubscriber) forwardLongPressCommand(resourceID string, msg LogicalResourceCommandMQTTPayload) {
	cmd := LongPressCommand{
		Kind: "event",
		Cmd:  "longPressCommand",
		Payload: LongPressCommandPayload{
			ResourceID:  msg.ResourceID,
			Timestamp:   msg.Timestamp,
			ThresholdMs: msg.DurationMs,
		},
	}
	if err := s.ipcBridge.writeJSON(cmd); err != nil {
		logging.Error("ResourceCmdSubscriber: failed to forward longPress to runtime", "resourceId", resourceID, "error", err)
	}
}

func (s *ResourceCmdSubscriber) forwardTimerCommand(resourceID string, msg LogicalResourceCommandMQTTPayload) {
	cmd := TimerCommand{
		Kind: "event",
		Cmd:  "timerCommand",
		Payload: TimerCommandPayload{
			ResourceID: msg.ResourceID,
			Action:     msg.Action,
			DurationMs: msg.DurationMs,
			Mode:       msg.Mode,
		},
	}
	if err := s.ipcBridge.writeJSON(cmd); err != nil {
		logging.Error("ResourceCmdSubscriber: failed to forward to runtime", "resourceId", resourceID, "error", err)
	}
}
