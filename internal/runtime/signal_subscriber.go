package runtime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/fisaks/uhn/internal/logging"
)

// SignalSubscriber implements messaging.Subscriber to handle incoming signal
// MQTT messages on the topic "signal/state/+".
// It catches master-originated signals and applies them to the IPC bridge,
// while filtering out self-published signals via SignalTracker.
type SignalSubscriber struct {
	ipcBridge     *IPCBridge
	signalTracker *SignalTracker
}

// NewSignalSubscriber creates a new signal subscriber.
func NewSignalSubscriber(ipcBridge *IPCBridge, signalTracker *SignalTracker) *SignalSubscriber {
	return &SignalSubscriber{
		ipcBridge:     ipcBridge,
		signalTracker: signalTracker,
	}
}

// OnMessage handles an incoming MQTT message on "signal/state/{resourceId}".
// Topic format after prefix stripping: "uhn/{edge}/signal/state/{resourceId}"
func (s *SignalSubscriber) OnMessage(ctx context.Context, topic string, payload []byte) {
	// Topic format: "uhn/{edge}/signal/state/{resourceId}"
	parts := strings.Split(topic, "/")
	if len(parts) != 5 {
		logging.Warn("SignalSubscriber: malformed topic", "topic", topic)
		return
	}
	resourceID := parts[4]

	var msg SignalMQTTPayload
	if err := json.Unmarshal(payload, &msg); err != nil {
		logging.Warn("SignalSubscriber: invalid JSON", "topic", topic, "error", err)
		return
	}

	// Skip self-published signals
	if s.signalTracker.IsEcho(resourceID, msg.Timestamp) {
		logging.Debug("SignalSubscriber: skipping self-echo", "resourceId", resourceID)
		return
	}

	logging.Debug("SignalSubscriber: applying signal", "resourceId", resourceID, "value", msg.Value)
	s.ipcBridge.HandleSignalUpdate(resourceID, msg.Value, msg.Timestamp)
}
