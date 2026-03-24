package runtime

import (
	"context"
	"encoding/json"

	"github.com/fisaks/uhn/internal/logging"
)

// MuteCmdSubscriber implements messaging.Subscriber to handle incoming mute
// command MQTT messages on the topic "mute/cmd".
// It receives mute commands from master and forwards them to the Node.js runtime via IPC.
type MuteCmdSubscriber struct {
	ipcBridge *IPCBridge
}

// NewMuteCmdSubscriber creates a new mute command subscriber.
func NewMuteCmdSubscriber(ipcBridge *IPCBridge) *MuteCmdSubscriber {
	return &MuteCmdSubscriber{
		ipcBridge: ipcBridge,
	}
}

// OnMessage handles an incoming MQTT message on "mute/cmd".
// Topic format after prefix stripping: "uhn/{edge}/mute/cmd"
func (s *MuteCmdSubscriber) OnMessage(ctx context.Context, topic string, payload []byte, _ bool) {
	var msg MuteMQTTPayload
	if err := json.Unmarshal(payload, &msg); err != nil {
		logging.Warn("MuteCmdSubscriber: invalid JSON", "topic", topic, "error", err)
		return
	}

	logging.Debug("MuteCmdSubscriber: received mute command",
		"targetType", msg.TargetType, "targetId", msg.TargetID, "action", msg.Action)

	// Forward to Node.js runtime via IPC
	cmd := MuteCommand{
		Kind: "event",
		Cmd:  "muteCommand",
		Payload: MuteCommandPayload{
			TargetType: msg.TargetType,
			TargetID:   msg.TargetID,
			Action:     msg.Action,
			ExpiresAt:  msg.ExpiresAt,
			Identifier: msg.Identifier,
		},
	}

	if err := s.ipcBridge.writeJSON(cmd); err != nil {
		logging.Error("MuteCmdSubscriber: failed to forward to runtime", "error", err)
	}
}
