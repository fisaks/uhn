package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
	"github.com/fisaks/uhn/internal/uhn"
)

// ActionHandler processes actions emitted by the edge rule runtime.
type ActionHandler interface {
	HandleRuntimeAction(ctx context.Context, action RuntimeAction, resource *RuntimeResource)
}

// IPCBridge connects Go polling to the Node.js rule runtime process via JSON-line IPC.
// It maintains the three-tier state model (physical / signal / computed) mirroring
// the master's StateRuntimeService.
type IPCBridge struct {
	edgeName  string
	deviceMap map[string]*config.DeviceConfig // device name -> config

	// Set after runtime starts
	stdin   io.Writer
	stdinMu sync.Mutex

	// Resource map (built after runtime ready)
	resourceMap   *ResourceMap
	resourceMapMu sync.RWMutex

	// Three-tier state per resourceId
	physicalState map[string]any // P
	signalState   map[string]any // S (nil = no signal)
	computedState map[string]any // C = S ?? P
	stateMu       sync.RWMutex

	// Address-keyed physical state cache (device:type:pin → value).
	// Populated unconditionally by UpdatePhysicalStateByAddress (no blueprint needed).
	// Read by transports for gatekeeper checks.
	physicalByAddress map[string]any

	actionHandler                    ActionHandler
	logicalResourceStatePublisher    *LogicalResourceStatePublisher
	logicalResourceStateSubscriber   *LogicalResourceStateSubscriber
	devicePinStatePublisher          *DevicePinStatePublisher
	onResourceMapReady               func(ctx context.Context) // called after ResourceMap is built

	broker messaging.Broker
}

// NewIPCBridge creates a new IPC bridge for the given edge.
func NewIPCBridge(edgeName string, deviceMap map[string]*config.DeviceConfig) *IPCBridge {
	return &IPCBridge{
		edgeName:          edgeName,
		deviceMap:         deviceMap,
		physicalState:     make(map[string]any),
		signalState:       make(map[string]any),
		computedState:     make(map[string]any),
		physicalByAddress: make(map[string]any),
	}
}

// SetActionHandler sets the action handler (called after pollers are created).
func (b *IPCBridge) SetActionHandler(handler ActionHandler) {
	b.actionHandler = handler
}

// SetLogicalResourceStatePublisher sets the logical resource state publisher for MQTT.
func (b *IPCBridge) SetLogicalResourceStatePublisher(pub *LogicalResourceStatePublisher) {
	b.logicalResourceStatePublisher = pub
}

// SetLogicalResourceStateSubscriber sets the subscriber used to restore logical resource state on restart.
func (b *IPCBridge) SetLogicalResourceStateSubscriber(sub *LogicalResourceStateSubscriber) {
	b.logicalResourceStateSubscriber = sub
}

// SetDevicePinStatePublisher sets the publisher for per-pin physical state MQTT messages.
// Used by IHC and future drivers that produce individual typed values.
func (b *IPCBridge) SetDevicePinStatePublisher(pub *DevicePinStatePublisher) {
	b.devicePinStatePublisher = pub
}

// SetOnResourceMapReady sets a callback invoked after the ResourceMap is built.
// Used by Z2M transport to replay cached state.
func (b *IPCBridge) SetOnResourceMapReady(fn func(ctx context.Context)) {
	b.onResourceMapReady = fn
}

// SetBroker sets the MQTT broker for publishing runtime rules.
func (b *IPCBridge) SetBroker(broker messaging.Broker) {
	b.broker = broker
}

// SetStdin provides the stdin writer for the runtime process.
func (b *IPCBridge) SetStdin(w io.Writer) {
	b.stdinMu.Lock()
	defer b.stdinMu.Unlock()
	b.stdin = w
}

// runtimeMessage is the minimal envelope shared by all runtime stdout messages.
type runtimeMessage struct {
	Kind string `json:"kind"` // "event" | "response"
	Cmd  string `json:"cmd"`  // "ready", "log", "actions", etc.
	ID   string `json:"id"`   // set on responses
}

// ProcessStdout reads JSON lines from the runtime's stdout, handling
// ready signals, log events, actions, and request/response correlation.
// It replaces supervisor.scanStdout().
func (b *IPCBridge) ProcessStdout(ctx context.Context, pipe io.Reader, readyCh chan<- bool) {
	scanner := bufio.NewScanner(pipe)
	readySent := false

	for scanner.Scan() {
		line := scanner.Bytes()

		var msg runtimeMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			logging.Info("Rule runtime stdout (non-JSON)", "line", string(line))
			continue
		}

		switch {
		// Ready signal
		case msg.Kind == "event" && msg.Cmd == "ready" && !readySent:
			readySent = true
			readyCh <- true
			go b.onReady(ctx)

		// Rules loaded event
		case msg.Kind == "event" && msg.Cmd == "rulesLoaded":
			b.handleRulesLoaded(ctx, line)

		// Resources loaded event
		case msg.Kind == "event" && msg.Cmd == "resourcesLoaded":
			b.handleResourcesLoaded(ctx, line)

		// Log event
		case msg.Kind == "event" && msg.Cmd == "log":
			b.handleLog(line)

		// Resource missing — rule tried to access a resource with no state on this edge
		case msg.Kind == "event" && msg.Cmd == "resourceMissing":
			b.handleResourceMissing(line)

		// Logical resource state changed event
		case msg.Kind == "event" && msg.Cmd == "logicalResourceStateChanged":
			b.handleLogicalResourceStateChanged(ctx, line)

		// Actions event
		case msg.Kind == "event" && msg.Cmd == "actions":
			b.handleActions(ctx, line)

		default:
			logging.Info("Rule runtime stdout (unhandled)", "line", string(line))
		}
	}

	if !readySent {
		readyCh <- false
	}
}

// onReady is called when the runtime sends the "ready" event.
// It restores retained timer state and sends the initial full state update
// followed by timer restore commands. The resource map is already built by
// handleResourcesLoaded which fires before ready.
func (b *IPCBridge) onReady(ctx context.Context) {
	// Apply retained timer states to computedState before sending the full update
	timerCommands := b.prepareTimerRestoration()

	b.sendFullStateUpdate()

	// Send timer restore commands AFTER full state update so the runtime
	// has baseline state before timers start ticking
	for _, cmd := range timerCommands {
		if err := b.writeJSON(cmd); err != nil {
			logging.Error("Failed to send timer restore command", "resourceId", cmd.Payload.ResourceID, "error", err)
		}
	}
}

// prepareTimerRestoration drains buffered retained logical resource states, applies them to
// computedState, and returns timerCommand messages to send after stateFullUpdate.
func (b *IPCBridge) prepareTimerRestoration() []TimerCommand {
	if b.logicalResourceStateSubscriber == nil {
		return nil
	}

	retained := b.logicalResourceStateSubscriber.DrainBuffered()
	if len(retained) == 0 {
		return nil
	}

	now := time.Now().UnixMilli()
	var commands []TimerCommand

	b.stateMu.Lock()
	for _, t := range retained {
		// Set baseline computed value for all logical resources
		b.computedState[t.ResourceID] = t.Value

		// Timer-specific restoration: resume or fire expired timers
		if t.Details != nil && t.Details.Type == "timer" {
			active, ok := t.Value.(bool)
			if ok && active {
				remaining := t.Details.StopAt - now
				var durationMs int64
				if remaining > 0 {
					durationMs = remaining
					logging.Info("Timer restoration: resuming active timer",
						"resourceId", t.ResourceID, "remainingMs", remaining)
				} else {
					durationMs = 1
					logging.Info("Timer restoration: expired during downtime, firing immediately",
						"resourceId", t.ResourceID, "expiredAgoMs", -remaining)
				}

				commands = append(commands, TimerCommand{
					Kind: "event",
					Cmd:  "timerCommand",
					Payload: TimerCommandPayload{
						ResourceID: t.ResourceID,
						Action:     "start",
						DurationMs: durationMs,
						Mode:       "restart",
					},
				})
			} else {
				logging.Debug("Timer restoration: inactive timer", "resourceId", t.ResourceID)
			}
		} else {
			logging.Debug("Logical resource state restoration: baseline value set", "resourceId", t.ResourceID, "value", t.Value)
		}
	}
	b.stateMu.Unlock()

	logging.Info("Logical resource state restoration complete", "retained", len(retained), "commands", len(commands))
	return commands
}

// HandleDeviceState converts polled device state into per-resource updates and
// sends changed resources to the runtime. Called by BridgedPublisher.
func (b *IPCBridge) HandleDeviceState(ctx context.Context, state uhn.DeviceState) {
	rm := b.getResourceMap()
	if rm == nil {
		return // runtime not ready yet
	}

	deviceCfg, ok := b.deviceMap[state.Name]
	if !ok {
		return
	}

	resourceStates := ExtractResourceStates(state, deviceCfg, rm, state.TimestampMs)

	for _, rs := range resourceStates {
		b.updatePhysicalState(rs.ResourceID, rs.Value, rs.Timestamp)
	}
}

// UpdatePhysicalStateByAddress caches state by address, publishes per-pin
// physical state to MQTT, and (if a ResourceMap is available) resolves the
// address to a resource ID and updates the local runtime state.
//
// Callers are responsible for pre-filtering — IHC publishes all notifications
// unconditionally, Z2M filters by ResourceMap (only blueprint-relevant
// properties). The physicalByAddress cache is always updated regardless
// (used for gatekeeper checks).
func (b *IPCBridge) UpdatePhysicalStateByAddress(ctx context.Context, device, resourceType string, pin any, value any, timestamp int64) {
	// Update address-keyed cache (unconditional, no blueprint needed)
	addrKey := fmt.Sprintf("%s:%s:%s", device, resourceType, formatPinForKey(pin))
	b.stateMu.Lock()
	b.physicalByAddress[addrKey] = value
	b.stateMu.Unlock()

	// Publish physical pin state to MQTT (independent of blueprint)
	if b.devicePinStatePublisher != nil {
		b.devicePinStatePublisher.Publish(ctx, device, resourceType, pin, value, timestamp)
	}

	// Update local runtime state (requires ResourceMap from active blueprint)
	rm := b.getResourceMap()
	if rm == nil {
		return
	}

	resourceID, ok := rm.LookupResourceID(device, resourceType, pin)
	if !ok {
		return
	}

	b.updatePhysicalState(resourceID, value, timestamp)
}

// ReadPhysicalStateByAddress reads the latest physical state for a device address.
// Returns (value, true) if state exists, (nil, false) if unknown.
func (b *IPCBridge) ReadPhysicalStateByAddress(device, resourceType string, pin any) (any, bool) {
	addrKey := fmt.Sprintf("%s:%s:%s", device, resourceType, formatPinForKey(pin))
	b.stateMu.RLock()
	v, ok := b.physicalByAddress[addrKey]
	b.stateMu.RUnlock()
	return v, ok
}

// HasResourceMap returns true if the ResourceMap has been built (blueprint loaded).
func (b *IPCBridge) HasResourceMap() bool {
	return b.getResourceMap() != nil
}

// GetDecimalPrecisionForAddress returns the decimal precision for a resource, or -1 if not set.
func (b *IPCBridge) GetDecimalPrecisionForAddress(device, resourceType string, pin any) int {
	rm := b.getResourceMap()
	if rm == nil {
		return -1
	}
	r, ok := rm.LookupByAddress(device, resourceType, pin)
	if !ok || r.DecimalPrecision == nil {
		return -1
	}
	return *r.DecimalPrecision
}

// HasResourceForAddress returns true if the current blueprint has a resource
// matching the given device/type/pin address. Used by Z2M transport to filter
// properties before publishing to MQTT.
func (b *IPCBridge) HasResourceForAddress(device, resourceType string, pin any) bool {
	rm := b.getResourceMap()
	if rm == nil {
		return false
	}
	_, ok := rm.LookupResourceID(device, resourceType, pin)
	return ok
}

// getResourceMap returns the current resource map (nil if runtime not ready).
func (b *IPCBridge) getResourceMap() *ResourceMap {
	b.resourceMapMu.RLock()
	defer b.resourceMapMu.RUnlock()
	return b.resourceMap
}

// HandleSignalUpdate updates the signal state for a resource, recomputes
// the computed state, and sends a stateUpdate to the runtime.
func (b *IPCBridge) HandleSignalUpdate(resourceID string, value any, timestamp int64) {
	b.stateMu.Lock()

	b.signalState[resourceID] = value

	physical := b.physicalState[resourceID]
	computed := value
	if computed == nil {
		computed = physical
	}
	prevComputed := b.computedState[resourceID]
	b.computedState[resourceID] = computed

	b.stateMu.Unlock()

	if computed != prevComputed {
		b.sendStateUpdate(RuntimeResourceState{
			ResourceID: resourceID,
			Value:      computed,
			Timestamp:  timestamp,
		})
	}
}

// updatePhysicalState updates P for a resource, clears signal if physical changed,
// recomputes C, and sends stateUpdate. Mirrors master's updatePhysicalState().
func (b *IPCBridge) updatePhysicalState(resourceID string, value any, timestamp int64) {
	b.stateMu.Lock()

	prevPhysical, hadPhysical := b.physicalState[resourceID]
	b.physicalState[resourceID] = value

	// Clear signal when physical state actually changes
	physicalChanged := hadPhysical && prevPhysical != value
	signal := b.signalState[resourceID]
	hadSignal := signal != nil

	if hadSignal && physicalChanged {
		delete(b.signalState, resourceID)
		signal = nil
	}

	// Compute: signal ?? physical
	computed := signal
	if computed == nil {
		computed = value
	}
	prevComputed := b.computedState[resourceID]
	b.computedState[resourceID] = computed

	b.stateMu.Unlock()

	if computed != prevComputed {
		b.sendStateUpdate(RuntimeResourceState{
			ResourceID: resourceID,
			Value:      computed,
			Timestamp:  timestamp,
		})
	}
}

// sendStateUpdate sends a single resource state change to the runtime.
func (b *IPCBridge) sendStateUpdate(state RuntimeResourceState) {
	cmd := StateUpdateCommand{
		Kind:    "event",
		Cmd:     "stateUpdate",
		Payload: state,
	}
	if err := b.writeJSON(cmd); err != nil {
		logging.Error("Failed to send stateUpdate", "resourceId", state.ResourceID, "error", err)
	}
}

// InjectSyntheticState sends a stateUpdate to the rule runtime without
// modifying the physical/signal/computed state model. Used to simulate
// button press/release for tap/longPress commands on physical resources
// without a bypassSignalState driver (e.g. Modbus digitalInput (readonly)) and
// logical resources (complex, virtualDigitalInput).
func (b *IPCBridge) InjectSyntheticState(resourceID string, value any, timestamp int64) {
	b.sendStateUpdate(RuntimeResourceState{
		ResourceID: resourceID,
		Value:      value,
		Timestamp:  timestamp,
	})
}

// sendFullStateUpdate sends the complete computed state snapshot to the runtime.
func (b *IPCBridge) sendFullStateUpdate() {
	b.stateMu.RLock()
	states := make([]RuntimeResourceState, 0, len(b.computedState))
	now := time.Now().UnixMilli()
	for id, val := range b.computedState {
		states = append(states, RuntimeResourceState{
			ResourceID: id,
			Value:      val,
			Timestamp:  now,
		})
	}
	b.stateMu.RUnlock()

	cmd := StateFullUpdateCommand{
		Kind:    "event",
		Cmd:     "stateFullUpdate",
		Payload: states,
	}
	if err := b.writeJSON(cmd); err != nil {
		logging.Error("Failed to send stateFullUpdate", "error", err)
	}
}

// handleLog forwards a structured log from the runtime to the Go logger.
func (b *IPCBridge) handleLog(raw []byte) {
	var msg LogEvent
	if err := json.Unmarshal(raw, &msg); err != nil {
		logging.Error("Failed to parse log event", "error", err)
		return
	}
	prefix := "Rule runtime [" + msg.Component + "]: "
	args := make([]any, 0, 2)
	if msg.Data != nil {
		args = append(args, "data", msg.Data)
	}
	switch msg.Level {
	case "error":
		logging.Error(prefix+msg.Message, args...)
	case "warn":
		logging.Warn(prefix+msg.Message, args...)
	case "debug":
		logging.Debug(prefix+msg.Message, args...)
	case "trace":
		logging.Trace(prefix+msg.Message, args...)
	default:
		logging.Info(prefix+msg.Message, args...)
	}
}

// handleResourceMissing logs when a rule references a resource that has no state on this edge.
// This typically means the rule was incorrectly tagged as edge-executable while
// referencing resources from another edge.
func (b *IPCBridge) handleResourceMissing(raw []byte) {
	var msg ResourceMissingEvent
	if err := json.Unmarshal(raw, &msg); err != nil {
		logging.Error("Failed to parse resourceMissing event", "error", err)
		return
	}
	logging.Error("Rule references unavailable resource on this edge",
		"ruleId", msg.RuleID,
		"resourceId", msg.ResourceID,
		"resourceType", msg.ResourceType,
		"reason", msg.Reason,
	)
}

// handleLogicalResourceStateChanged publishes logical resource state to MQTT and updates local computed state.
func (b *IPCBridge) handleLogicalResourceStateChanged(ctx context.Context, raw []byte) {
	var event LogicalResourceStateChangedEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		logging.Error("Failed to parse logicalResourceStateChanged event", "error", err)
		return
	}

	state := event.Payload
	timestamp := time.Now().UnixMilli()

	// Update local computed state so the runtime sees logical resource state
	b.stateMu.Lock()
	prevComputed := b.computedState[state.ResourceID]
	b.computedState[state.ResourceID] = state.Value
	b.stateMu.Unlock()

	if state.Value != prevComputed {
		b.sendStateUpdate(RuntimeResourceState{
			ResourceID: state.ResourceID,
			Value:      state.Value,
			Timestamp:  timestamp,
		})
	}

	// Publish to MQTT
	if b.logicalResourceStatePublisher != nil {
		b.logicalResourceStatePublisher.Publish(ctx, state, timestamp)
	}
}

// HandleSetState processes an incoming setState command for virtualDigitalInput resources.
// It forwards the value to the runtime as a stateUpdate, updates the local computed
// state, and publishes to MQTT so the master picks it up and broadcasts to the UI.
//
// This is only used for virtualDigitalInput resources — other resource types manage state
// through their own dedicated paths (physical polling, timer events, etc.).
// The master validates the resource type before sending setState commands.
func (b *IPCBridge) HandleSetState(ctx context.Context, resourceID string, value any, timestamp int64) {
	// Forward to runtime
	cmd := StateUpdateCommand{
		Kind: "event",
		Cmd:  "stateUpdate",
		Payload: RuntimeResourceState{
			ResourceID: resourceID,
			Value:      value,
			Timestamp:  timestamp,
		},
	}
	if err := b.writeJSON(cmd); err != nil {
		logging.Error("HandleSetState: failed to forward stateUpdate to runtime", "resourceId", resourceID, "error", err)
	}

	// Update local computed state
	b.stateMu.Lock()
	b.computedState[resourceID] = value
	b.stateMu.Unlock()

	// Publish to MQTT so master receives the state
	if b.logicalResourceStatePublisher != nil {
		b.logicalResourceStatePublisher.Publish(ctx, LogicalResourceStateChangedPayload{
			ResourceID: resourceID,
			Value:      value,
		}, timestamp)
	}
}

// handleActions processes an actions event from the runtime.
func (b *IPCBridge) handleActions(ctx context.Context, raw []byte) {
	var event ActionsEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		logging.Error("Failed to parse actions event", "error", err)
		return
	}

	if b.actionHandler == nil {
		logging.Warn("Actions received but no action handler set", "count", len(event.Actions))
		return
	}

	rm := b.getResourceMap()
	for _, action := range event.Actions {
		var resource *RuntimeResource
		// Mute actions don't reference a resource by resourceId
		if rm != nil && action.ResourceID != "" {
			resource, _ = rm.LookupResource(action.ResourceID)
		}
		b.actionHandler.HandleRuntimeAction(ctx, action, resource)
	}
}

// handleRulesLoaded publishes the loaded rule count to MQTT so master can compare expected vs actual.
func (b *IPCBridge) handleRulesLoaded(ctx context.Context, raw []byte) {
	var event RulesLoadedEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		logging.Error("Failed to parse rulesLoaded event", "error", err)
		return
	}

	count := len(event.Rules)
	ids := make([]string, len(event.Rules))
	for i, r := range event.Rules {
		ids[i] = r.ID
	}
	logging.Info("Rule runtime reported loaded rules", "count", count, "ruleIds", ids)

	if b.broker != nil {
		payload := []byte(fmt.Sprintf("%d", count))
		if err := b.broker.Publish(ctx, "runtime/rules", messaging.AtLeastOnce, true, payload); err != nil {
			logging.Error("Failed to publish runtime rule count to MQTT", "error", err)
		}
	}
}

// handleResourcesLoaded processes the resourcesLoaded event and builds the resource map.
func (b *IPCBridge) handleResourcesLoaded(ctx context.Context, raw []byte) {
	var event ResourcesLoadedEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		logging.Error("Failed to parse resourcesLoaded event", "error", err)
		return
	}

	rm := NewResourceMap(b.edgeName, event.Resources)

	b.resourceMapMu.Lock()
	b.resourceMap = rm
	b.resourceMapMu.Unlock()

	logging.Info("IPC bridge resource map built", "resources", len(rm.byResourceID))

	// Notify listeners (e.g. Z2M transport replays cached state)
	if b.onResourceMapReady != nil {
		b.onResourceMapReady(ctx)
	}
}

// clearRuntimeRules publishes an empty retained message to clear the runtime/rules topic.
func (b *IPCBridge) clearRuntimeRules(ctx context.Context) {
	if b.broker != nil {
		if err := b.broker.Publish(ctx, "runtime/rules", messaging.AtLeastOnce, true, []byte{}); err != nil {
			logging.Error("Failed to clear runtime rules from MQTT", "error", err)
		}
	}
}

// EmitActionEvent forwards a transient action event to the edge runtime and publishes
// it to MQTT so the master runtime can also process it.
// Looks up the resource by device address (type "actionInput") to get the resourceId,
// then writes an actionInputEvent IPC command. Completely bypasses the P/S/C state model.
func (b *IPCBridge) EmitActionEvent(ctx context.Context, device string, pin string, action string, metadata map[string]any, timestamp int64) {
	rm := b.getResourceMap()
	if rm == nil {
		return
	}

	resourceID, ok := rm.LookupResourceID(device, "actionInput", pin)
	if !ok {
		return
	}

	cmd := ActionEventCommand{
		Kind: "event",
		Cmd:  "actionEvent",
		Payload: ActionEventPayload{
			ResourceID: resourceID,
			Action:     action,
			Metadata:   metadata,
			Timestamp:  timestamp,
		},
	}
	if err := b.writeJSON(cmd); err != nil {
		logging.Error("Failed to send actionInputEvent", "resourceId", resourceID, "action", action, "error", err)
	}

	// Publish to MQTT so master runtime can also process the action event
	if b.broker != nil {
		topic := fmt.Sprintf("device/%s/action/%s", device, pin)
		payload := map[string]any{
			"action":    action,
			"timestamp": timestamp,
		}
		if metadata != nil {
			payload["metadata"] = metadata
		}
		if err := b.broker.PublishJSON(ctx, topic, messaging.AtLeastOnce, false, payload); err != nil {
			logging.Error("Failed to publish action event to MQTT", "resourceId", resourceID, "action", action, "error", err)
		}
	}

	logging.Debug("Action event emitted to runtime", "resourceId", resourceID, "device", device, "pin", pin, "action", action)
}

// writeJSON marshals v to JSON and writes it as a newline-terminated line to stdin.
func (b *IPCBridge) writeJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	b.stdinMu.Lock()
	defer b.stdinMu.Unlock()

	if b.stdin == nil {
		return fmt.Errorf("stdin not available")
	}
	_, err = b.stdin.Write(data)
	return err
}

// Reset clears all state when the runtime is stopped/restarted.
func (b *IPCBridge) Reset(ctx context.Context) {
	b.stdinMu.Lock()
	b.stdin = nil
	b.stdinMu.Unlock()

	b.resourceMapMu.Lock()
	b.resourceMap = nil
	b.resourceMapMu.Unlock()

	b.stateMu.Lock()
	b.physicalState = make(map[string]any)
	b.signalState = make(map[string]any)
	b.computedState = make(map[string]any)
	b.stateMu.Unlock()

	// Clear retained runtime rules from MQTT
	b.clearRuntimeRules(ctx)

	// Re-subscribe to logical-resource/state/+ so retained messages are re-captured
	// for the next runtime startup
	if b.logicalResourceStateSubscriber != nil {
		b.logicalResourceStateSubscriber.Resubscribe(ctx)
	}
}
