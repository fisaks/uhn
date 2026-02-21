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
