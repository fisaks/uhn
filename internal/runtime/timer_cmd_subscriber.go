package runtime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/fisaks/uhn/internal/logging"
)

// TimerCmdSubscriber implements messaging.Subscriber to handle incoming timer
// command MQTT messages on the topic "timer/cmd/+".
// It receives commands from master and forwards them to the Node.js runtime via IPC.
type TimerCmdSubscriber struct {
	ipcBridge     *IPCBridge
	signalTracker *SignalTracker
}

// NewTimerCmdSubscriber creates a new timer command subscriber.
func NewTimerCmdSubscriber(ipcBridge *IPCBridge, signalTracker *SignalTracker) *TimerCmdSubscriber {
	return &TimerCmdSubscriber{
		ipcBridge:     ipcBridge,
		signalTracker: signalTracker,
	}
}

// OnMessage handles an incoming MQTT message on "timer/cmd/{resourceId}".
// Topic format after prefix stripping: "uhn/{edge}/timer/cmd/{resourceId}"
func (s *TimerCmdSubscriber) OnMessage(ctx context.Context, topic string, payload []byte) {
	// Topic format: "uhn/{edge}/timer/cmd/{resourceId}"
	parts := strings.Split(topic, "/")
	if len(parts) != 5 {
		logging.Warn("TimerCmdSubscriber: malformed topic", "topic", topic)
		return
	}
	resourceID := parts[4]

	var msg TimerCommandMQTTPayload
	if err := json.Unmarshal(payload, &msg); err != nil {
		logging.Warn("TimerCmdSubscriber: invalid JSON", "topic", topic, "error", err)
		return
	}

	logging.Debug("TimerCmdSubscriber: received timer command", "resourceId", resourceID, "action", msg.Action)

	// Forward to Node.js runtime via IPC
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
		logging.Error("TimerCmdSubscriber: failed to forward to runtime", "resourceId", resourceID, "error", err)
	}
}
