package runtime

import (
	"context"
	"encoding/json"

	"github.com/fisaks/uhn/internal/logging"
)

// ScheduleEventSubscriber implements messaging.Subscriber to handle incoming
// schedule fired MQTT messages on the topic "uhn/master/schedule/fired".
// It receives schedule fire events from master and forwards them to the
// Node.js runtime via IPC so edge-located rules with onSchedule() triggers can execute.
type ScheduleEventSubscriber struct {
	ipcBridge *IPCBridge
}

// NewScheduleEventSubscriber creates a new schedule event subscriber.
func NewScheduleEventSubscriber(ipcBridge *IPCBridge) *ScheduleEventSubscriber {
	return &ScheduleEventSubscriber{
		ipcBridge: ipcBridge,
	}
}

// OnMessage handles an incoming MQTT message on "uhn/master/schedule/fired".
func (s *ScheduleEventSubscriber) OnMessage(ctx context.Context, topic string, payload []byte, _ bool) {
	var msg ScheduleFiredMQTTPayload
	if err := json.Unmarshal(payload, &msg); err != nil {
		logging.Warn("ScheduleEventSubscriber: invalid JSON", "topic", topic, "error", err)
		return
	}

	logging.Debug("ScheduleEventSubscriber: received schedule fired event",
		"scheduleId", msg.ScheduleID, "firedAt", msg.FiredAt)

	// Forward to Node.js runtime via IPC
	cmd := ScheduleEventCommand{
		Kind: "event",
		Cmd:  "scheduleEvent",
		Payload: ScheduleEventPayload{
			ScheduleID: msg.ScheduleID,
			FiredAt:    msg.FiredAt,
		},
	}

	if err := s.ipcBridge.writeJSON(cmd); err != nil {
		logging.Error("ScheduleEventSubscriber: failed to forward to runtime", "scheduleId", msg.ScheduleID, "error", err)
	}
}
