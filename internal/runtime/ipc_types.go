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
	Type   string `json:"type"`
	Edge   string `json:"edge,omitempty"`
	Device string `json:"device,omitempty"`
	Pin    *int   `json:"pin,omitempty"`
	Host   string `json:"host,omitempty"`
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

// --- Logical resource state IPC types ---

// LogicalResourceStateChangedEvent is emitted by the runtime when a logical resource changes state.
type LogicalResourceStateChangedEvent struct {
	Kind    string                              `json:"kind"` // "event"
	Cmd     string                              `json:"cmd"`  // "logicalResourceStateChanged"
	Payload LogicalResourceStateChangedPayload  `json:"payload"`
}

// LogicalResourceStateChangedPayload is the payload of a LogicalResourceStateChangedEvent.
type LogicalResourceStateChangedPayload struct {
	ResourceID string                       `json:"resourceId"`
	Value      any                          `json:"value"`
	Timestamp  int64                        `json:"timestamp"`
	Details    *LogicalResourceStateDetails `json:"details,omitempty"`
}

// LogicalResourceStateDetails carries type-specific metadata for logical resources.
type LogicalResourceStateDetails struct {
	Type      string `json:"type"`                // "timer"
	StartedAt int64  `json:"startedAt,omitempty"`
	StopAt    int64  `json:"stopAt,omitempty"`
}

// LogicalResourceMQTTPayload is the JSON payload for logical resource state MQTT messages.
type LogicalResourceMQTTPayload struct {
	ResourceID string                       `json:"resourceId"`
	Value      any                          `json:"value"`
	Timestamp  int64                        `json:"timestamp"`
	Details    *LogicalResourceStateDetails `json:"details,omitempty"`
}

// LogicalResourceCommandMQTTPayload is the JSON payload for logical resource command MQTT messages
// on the logical-resource/cmd/+ topic.
type LogicalResourceCommandMQTTPayload struct {
	ResourceID string `json:"resourceId"`
	Action     string `json:"action"` // "start" | "clear" | "tap"
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

// --- Tap IPC types ---

// TapCommand is the IPC command sent to the runtime to emit a tap event for a complex resource.
type TapCommand struct {
	Kind    string            `json:"kind"` // "event"
	Cmd     string            `json:"cmd"`  // "tapCommand"
	Payload TapCommandPayload `json:"payload"`
}

// TapCommandPayload is the payload of a TapCommand.
type TapCommandPayload struct {
	ResourceID string `json:"resourceId"`
	Timestamp  int64  `json:"timestamp"`
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
