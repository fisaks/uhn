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
	"github.com/fisaks/uhn/internal/uhn"
)

const listResourcesTimeout = 10 * time.Second

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

	// Pending request/response
	pendingMu       sync.Mutex
	pendingRequests map[string]chan json.RawMessage

	actionHandler ActionHandler
}

// NewIPCBridge creates a new IPC bridge for the given edge.
func NewIPCBridge(edgeName string, deviceMap map[string]*config.DeviceConfig) *IPCBridge {
	return &IPCBridge{
		edgeName:        edgeName,
		deviceMap:       deviceMap,
		physicalState:   make(map[string]any),
		signalState:     make(map[string]any),
		computedState:   make(map[string]any),
		pendingRequests: make(map[string]chan json.RawMessage),
	}
}

// SetActionHandler sets the action handler (called after pollers are created).
func (b *IPCBridge) SetActionHandler(handler ActionHandler) {
	b.actionHandler = handler
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

		// Log event
		case msg.Kind == "event" && msg.Cmd == "log":
			b.handleLog(line)

		// Resource missing — rule tried to access a resource with no state on this edge
		case msg.Kind == "event" && msg.Cmd == "resourceMissing":
			b.handleResourceMissing(line)

		// Actions event
		case msg.Kind == "event" && msg.Cmd == "actions":
			b.handleActions(ctx, line)

		// Response to a request
		case msg.Kind == "response" && msg.ID != "":
			b.pendingMu.Lock()
			ch, ok := b.pendingRequests[msg.ID]
			if ok {
				delete(b.pendingRequests, msg.ID)
			}
			b.pendingMu.Unlock()
			if ok {
				ch <- json.RawMessage(line)
			}

		default:
			logging.Info("Rule runtime stdout (unhandled)", "line", string(line))
		}
	}

	if !readySent {
		readyCh <- false
	}
}

// onReady is called when the runtime sends the "ready" event.
// It requests the resource list and sends the initial full state update.
func (b *IPCBridge) onReady(ctx context.Context) {
	resources, err := b.requestListResources(ctx)
	if err != nil {
		logging.Error("Failed to list resources from runtime", "error", err)
		return
	}

	rm := NewResourceMap(b.edgeName, resources)
	b.resourceMapMu.Lock()
	b.resourceMap = rm
	b.resourceMapMu.Unlock()

	logging.Info("IPC bridge resource map built", "resources", len(rm.byResourceID))

	b.sendFullStateUpdate()
}

// HandleDeviceState converts polled device state into per-resource updates and
// sends changed resources to the runtime. Called by BridgedPublisher.
func (b *IPCBridge) HandleDeviceState(ctx context.Context, state uhn.DeviceState) {
	b.resourceMapMu.RLock()
	rm := b.resourceMap
	b.resourceMapMu.RUnlock()

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

// requestListResources sends a listResources request and waits for the response.
func (b *IPCBridge) requestListResources(ctx context.Context) ([]RuntimeResource, error) {
	id := fmt.Sprintf("lr-%d", time.Now().UnixMilli())

	ch := make(chan json.RawMessage, 1)
	b.pendingMu.Lock()
	b.pendingRequests[id] = ch
	b.pendingMu.Unlock()

	cmd := ListResourcesCommand{
		Kind: "request",
		ID:   id,
		Cmd:  "listResources",
	}
	if err := b.writeJSON(cmd); err != nil {
		b.pendingMu.Lock()
		delete(b.pendingRequests, id)
		b.pendingMu.Unlock()
		return nil, fmt.Errorf("write listResources: %w", err)
	}

	select {
	case raw := <-ch:
		var resp ListResourcesResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("parse listResources response: %w", err)
		}
		return resp.Resources, nil
	case <-time.After(listResourcesTimeout):
		b.pendingMu.Lock()
		delete(b.pendingRequests, id)
		b.pendingMu.Unlock()
		return nil, fmt.Errorf("listResources timeout after %v", listResourcesTimeout)
	case <-ctx.Done():
		b.pendingMu.Lock()
		delete(b.pendingRequests, id)
		b.pendingMu.Unlock()
		return nil, ctx.Err()
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
	switch msg.Level {
	case "error":
		logging.Error(prefix + msg.Message)
	case "warn":
		logging.Warn(prefix + msg.Message)
	default:
		logging.Info(prefix + msg.Message)
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

	b.resourceMapMu.RLock()
	rm := b.resourceMap
	b.resourceMapMu.RUnlock()

	for _, action := range event.Actions {
		var resource *RuntimeResource
		if rm != nil {
			resource, _ = rm.LookupResource(action.ResourceID)
		}
		b.actionHandler.HandleRuntimeAction(ctx, action, resource)
	}
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
func (b *IPCBridge) Reset() {
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

	b.pendingMu.Lock()
	for id, ch := range b.pendingRequests {
		close(ch)
		delete(b.pendingRequests, id)
	}
	b.pendingMu.Unlock()
}
