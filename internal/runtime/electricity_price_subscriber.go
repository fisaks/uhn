package runtime

import (
	"context"
	"encoding/json"

	"github.com/fisaks/uhn/internal/logging"
)

// ElectricityPriceMQTTPayload mirrors ElectricityPriceSnapshot from uhn-common.
type ElectricityPriceMQTTPayload struct {
	Prices    json.RawMessage `json:"prices"`
	FetchedAt string          `json:"fetchedAt"`
}

// ElectricityPriceUpdateCommand is the IPC command sent to the runtime.
type ElectricityPriceUpdateCommand struct {
	Kind    string                      `json:"kind"` // "event"
	Cmd     string                      `json:"cmd"`  // "electricityPriceUpdate"
	Payload ElectricityPriceMQTTPayload `json:"payload"`
}

// ElectricityPriceSubscriber implements messaging.Subscriber to handle incoming
// electricity price MQTT messages from master and forward them to the edge runtime.
type ElectricityPriceSubscriber struct {
	ipcBridge *IPCBridge
}

// NewElectricityPriceSubscriber creates a new electricity price subscriber.
func NewElectricityPriceSubscriber(ipcBridge *IPCBridge) *ElectricityPriceSubscriber {
	return &ElectricityPriceSubscriber{
		ipcBridge: ipcBridge,
	}
}

// OnMessage handles an incoming MQTT message on "uhn/master/electricity/prices".
func (s *ElectricityPriceSubscriber) OnMessage(ctx context.Context, topic string, payload []byte, _ bool) {
	var msg ElectricityPriceMQTTPayload
	if err := json.Unmarshal(payload, &msg); err != nil {
		logging.Warn("ElectricityPriceSubscriber: invalid JSON", "topic", topic, "error", err)
		return
	}

	logging.Debug("ElectricityPriceSubscriber: received price update",
		"fetchedAt", msg.FetchedAt)

	cmd := ElectricityPriceUpdateCommand{
		Kind:    "event",
		Cmd:     "electricityPriceUpdate",
		Payload: msg,
	}

	if err := s.ipcBridge.writeJSON(cmd); err != nil {
		logging.Error("ElectricityPriceSubscriber: failed to forward to runtime", "error", err)
	}
}
