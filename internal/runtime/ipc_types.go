package runtime

// IPC protocol types mirroring the TypeScript definitions in
// uxp/packages/uhn-common/src/types/uhn-runtime.type.ts

// RuntimeResourceState carries a single resource's value at a point in time.
type RuntimeResourceState struct {
	ResourceID string `json:"resourceId"`
	Value      any    `json:"value"`
	Timestamp  int64  `json:"timestamp"`
}

// RuntimeResource describes a resource known to the runtime.
type RuntimeResource struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Edge   string `json:"edge"`
	Device string `json:"device,omitempty"`
	Type   string `json:"type"`
	Pin    *int   `json:"pin,omitempty"`
}

// RuntimeAction is emitted by the rule engine when a rule fires.
type RuntimeAction struct {
	Type       string `json:"type"`       // "setOutput" | "emitSignal"
	ResourceID string `json:"resourceId"`
	Value      any    `json:"value"`
}

// --- Commands (Go → Runtime stdin) ---

// ListResourcesCommand requests the list of known resources.
type ListResourcesCommand struct {
	Kind string `json:"kind"` // "request"
	ID   string `json:"id"`
	Cmd  string `json:"cmd"` // "listResources"
}

// StateUpdateCommand sends a single resource state change.
type StateUpdateCommand struct {
	Kind    string               `json:"kind"` // "event"
	Cmd     string               `json:"cmd"`  // "stateUpdate"
	Payload RuntimeResourceState `json:"payload"`
}

// StateFullUpdateCommand sends the complete state snapshot.
type StateFullUpdateCommand struct {
	Kind    string                 `json:"kind"` // "event"
	Cmd     string                 `json:"cmd"`  // "stateFullUpdate"
	Payload []RuntimeResourceState `json:"payload"`
}

// --- Events/Responses (Runtime stdout → Go) ---

// ActionsEvent is emitted when rules produce actions.
type ActionsEvent struct {
	Kind    string          `json:"kind"` // "event"
	Cmd     string          `json:"cmd"`  // "actions"
	Actions []RuntimeAction `json:"actions"`
}

// ListResourcesResponse is the reply to a listResources request.
type ListResourcesResponse struct {
	Kind      string            `json:"kind"` // "response"
	ID        string            `json:"id"`
	Resources []RuntimeResource `json:"resources"`
}

// ResourceMissingEvent is emitted when a rule references a resource with no state.
type ResourceMissingEvent struct {
	Kind         string `json:"kind"` // "event"
	Cmd          string `json:"cmd"`  // "resourceMissing"
	RuleID       string `json:"ruleId"`
	ResourceID   string `json:"resourceId"`
	ResourceType string `json:"resourceType"`
	Reason       string `json:"reason"`
}

// LogEvent is a structured log emitted by the runtime.
type LogEvent struct {
	Kind      string `json:"kind"` // "event"
	Cmd       string `json:"cmd"`  // "log"
	Level     string `json:"level"`
	Component string `json:"component"`
	Message   string `json:"message"`
}

// --- Timer IPC types ---

// TimerStateChangedEvent is emitted by the runtime when a timer changes state.
type TimerStateChangedEvent struct {
	Kind    string            `json:"kind"` // "event"
	Cmd     string            `json:"cmd"`  // "timerStateChanged"
	Payload TimerRuntimeState `json:"payload"`
}

// TimerRuntimeState represents the state of a timer resource.
type TimerRuntimeState struct {
	ID        string `json:"id"`
	Active    bool   `json:"active"`
	StartedAt int64  `json:"startedAt"`
	StopAt    int64  `json:"stopAt"`
}

// TimerMQTTPayload is the JSON payload for timer state MQTT messages.
type TimerMQTTPayload struct {
	ResourceID string `json:"resourceId"`
	Active     bool   `json:"active"`
	StartedAt  int64  `json:"startedAt"`
	StopAt     int64  `json:"stopAt"`
	Timestamp  int64  `json:"timestamp"`
}

// TimerCommandMQTTPayload is the JSON payload for timer command MQTT messages.
type TimerCommandMQTTPayload struct {
	ResourceID string `json:"resourceId"`
	Action     string `json:"action"` // "start" | "clear"
	DurationMs int64  `json:"durationMs,omitempty"`
	Mode       string `json:"mode,omitempty"` // "restart" | "startOnce"
	Timestamp  int64  `json:"timestamp"`
}

// TimerCommand is the IPC command sent to the runtime to control a timer.
type TimerCommand struct {
	Kind    string              `json:"kind"` // "event"
	Cmd     string              `json:"cmd"`  // "timerCommand"
	Payload TimerCommandPayload `json:"payload"`
}

// TimerCommandPayload is the payload of a TimerCommand.
type TimerCommandPayload struct {
	ResourceID string `json:"resourceId"`
	Action     string `json:"action"` // "start" | "clear"
	DurationMs int64  `json:"durationMs,omitempty"`
	Mode       string `json:"mode,omitempty"` // "restart" | "startOnce"
}
