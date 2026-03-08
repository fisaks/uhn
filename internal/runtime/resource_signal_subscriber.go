package runtime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/fisaks/uhn/internal/logging"
)

// ResourceSignalSubscriber implements messaging.Subscriber to handle incoming
// signal override MQTT messages on the topic "resource/signal/+".
// It catches master-originated signals and applies them to the IPC bridge,
// while filtering out self-published signals via SignalTracker.
type ResourceSignalSubscriber struct {
	ipcBridge     *IPCBridge
	signalTracker *SignalTracker
}

// NewResourceSignalSubscriber creates a new resource signal subscriber.
func NewResourceSignalSubscriber(ipcBridge *IPCBridge, signalTracker *SignalTracker) *ResourceSignalSubscriber {
	return &ResourceSignalSubscriber{
		ipcBridge:     ipcBridge,
		signalTracker: signalTracker,
	}
}

// OnMessage handles an incoming MQTT message on "resource/signal/{resourceId}".
// Topic format after prefix stripping: "uhn/{edge}/resource/signal/{resourceId}"
func (s *ResourceSignalSubscriber) OnMessage(ctx context.Context, topic string, payload []byte) {
	// Topic format: "uhn/{edge}/resource/signal/{resourceId}"
	parts := strings.Split(topic, "/")
	if len(parts) != 5 {
		logging.Warn("ResourceSignalSubscriber: malformed topic", "topic", topic)
		return
	}
	resourceID := parts[4]

	var msg SignalMQTTPayload
	if err := json.Unmarshal(payload, &msg); err != nil {
		logging.Warn("ResourceSignalSubscriber: invalid JSON", "topic", topic, "error", err)
		return
	}

	// Skip self-published signals
	if s.signalTracker.IsEcho(resourceID, msg.Timestamp) {
		logging.Debug("ResourceSignalSubscriber: skipping self-echo", "resourceId", resourceID)
		return
	}

	logging.Debug("ResourceSignalSubscriber: applying signal", "resourceId", resourceID, "value", msg.Value)
	s.ipcBridge.HandleSignalUpdate(resourceID, msg.Value, msg.Timestamp)
}
