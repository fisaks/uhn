package runtime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/fisaks/uhn/internal/logging"
)

// LogicalResourceCmdSubscriber implements messaging.Subscriber to handle incoming
// logical resource command MQTT messages on the topic "logical-resource/cmd/+".
// It receives commands from master and forwards them to the Node.js runtime via IPC.
type LogicalResourceCmdSubscriber struct {
	ipcBridge     *IPCBridge
	signalTracker *SignalTracker
}

// NewLogicalResourceCmdSubscriber creates a new logical resource command subscriber.
func NewLogicalResourceCmdSubscriber(ipcBridge *IPCBridge, signalTracker *SignalTracker) *LogicalResourceCmdSubscriber {
	return &LogicalResourceCmdSubscriber{
		ipcBridge:     ipcBridge,
		signalTracker: signalTracker,
	}
}

// OnMessage handles an incoming MQTT message on "logical-resource/cmd/{resourceId}".
// Topic format after prefix stripping: "uhn/{edge}/logical-resource/cmd/{resourceId}"
func (s *LogicalResourceCmdSubscriber) OnMessage(ctx context.Context, topic string, payload []byte) {
	// Topic format: "uhn/{edge}/logical-resource/cmd/{resourceId}"
	parts := strings.Split(topic, "/")
	if len(parts) != 5 {
		logging.Warn("LogicalResourceCmdSubscriber: malformed topic", "topic", topic)
		return
	}
	resourceID := parts[4]

	var msg LogicalResourceCommandMQTTPayload
	if err := json.Unmarshal(payload, &msg); err != nil {
		logging.Warn("LogicalResourceCmdSubscriber: invalid JSON", "topic", topic, "error", err)
		return
	}

	logging.Debug("LogicalResourceCmdSubscriber: received command", "resourceId", resourceID, "action", msg.Action)

	// Forward to Node.js runtime via IPC (timerCommand IPC stays unchanged)
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
		logging.Error("LogicalResourceCmdSubscriber: failed to forward to runtime", "resourceId", resourceID, "error", err)
	}
}
