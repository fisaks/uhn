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
// Fields are a superset of all action types; only relevant fields are populated.
type RuntimeAction struct {
	Type       string `json:"type"`                 // "setDigitalOutput" | "setAnalogOutput" | "emitSignal" | "timerStart" | "timerClear" | "mute" | "clearMute"
	ResourceID string `json:"resourceId,omitempty"`
	Value      any    `json:"value,omitempty"`
	// Mute-specific fields
	TargetType string `json:"targetType,omitempty"` // "rule" | "resource"
	TargetID   string `json:"targetId,omitempty"`
	ExpiresAt  int64  `json:"expiresAt,omitempty"`
	Identifier string `json:"identifier,omitempty"`
}

// --- Commands (Go → Runtime stdin) ---

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

// ResourcesLoadedEvent is emitted by the runtime after resources are loaded.
type ResourcesLoadedEvent struct {
	Kind      string            `json:"kind"` // "event"
	Cmd       string            `json:"cmd"`  // "resourcesLoaded"
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

// --- Rules loaded IPC types ---

// RulesLoadedEvent is emitted by the runtime after rules are loaded.
// Go only uses the rule count and IDs — extra fields are ignored by json.Unmarshal.
type RulesLoadedEvent struct {
	Kind  string `json:"kind"` // "event"
	Cmd   string `json:"cmd"`  // "rulesLoaded"
	Rules []struct {
		ID string `json:"id"`
	} `json:"rules"`
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

// --- Mute IPC types ---

// MuteAction is emitted by the rule engine when a mute/clearMute action fires.
type MuteAction struct {
	Type       string `json:"type"`                 // "mute" | "clearMute"
	TargetType string `json:"targetType"`           // "rule" | "resource"
	TargetID   string `json:"targetId"`
	ExpiresAt  int64  `json:"expiresAt,omitempty"`
	Identifier string `json:"identifier,omitempty"`
}

// MuteMQTTPayload is the JSON payload for mute MQTT messages (both event and cmd).
type MuteMQTTPayload struct {
	TargetType string `json:"targetType"`           // "rule" | "resource"
	TargetID   string `json:"targetId"`
	Action     string `json:"action"`               // "mute" | "clearMute"
	ExpiresAt  int64  `json:"expiresAt,omitempty"`
	Identifier string `json:"identifier,omitempty"`
}

// MuteCommand is the IPC command sent to the runtime to apply a mute.
type MuteCommand struct {
	Kind    string             `json:"kind"` // "event"
	Cmd     string             `json:"cmd"`  // "muteCommand"
	Payload MuteCommandPayload `json:"payload"`
}

// MuteCommandPayload is the payload of a MuteCommand.
type MuteCommandPayload struct {
	TargetType string `json:"targetType"`           // "rule" | "resource"
	TargetID   string `json:"targetId"`
	Action     string `json:"action"`               // "mute" | "clearMute"
	ExpiresAt  int64  `json:"expiresAt,omitempty"`
	Identifier string `json:"identifier,omitempty"`
}
