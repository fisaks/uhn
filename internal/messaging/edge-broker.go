package messaging

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/state"
	"github.com/fisaks/uhn/internal/uhn"
)

type EdgeBroker interface {
	Broker
	uhn.EdgePublisher
	StartEdgeSubscriber(ctx context.Context, subscriber uhn.EdgeSubscriber)
	ClearPublishedState()
}
type edgeBroker struct {
	Broker
	uhn.EdgePublisher
	uhn.EdgeSubscriber
	Subscriber
	edgeState         state.EdgeStateStore
	heartbeatInterval time.Duration
}

func NewEdgeBroker(cfg BrokerConfig, catalog OnConnectPublisher, heartbeatInterval time.Duration) EdgeBroker {
	broker := NewBroker(cfg)
	edgeSate := state.NewEdgeStateStore()
	edgeBroker := &edgeBroker{
		Broker:            broker,
		heartbeatInterval: heartbeatInterval,
		edgeState:         edgeSate,
	}

	edgeBroker.AddOnConnectPublisher("catalog", catalog)
	edgeBroker.EdgePublisher = edgeBroker
	return edgeBroker
}

func (b *edgeBroker) StartEdgeSubscriber(ctx context.Context, subscriber uhn.EdgeSubscriber) {
	b.EdgeSubscriber = subscriber
	b.Subscribe(ctx, "device/+/cmd", AtLeastOnce, b)
	b.Subscribe(ctx, "cmd", AtLeastOnce, b)
}

func (b *edgeBroker) PublishDeviceState(ctx context.Context, state uhn.DeviceState) error {

	isChanged := b.edgeState.HasChanged(state.Name, state)
	needsHeartbeat := false
	if !isChanged {
		_, lastSent, hasPrev := b.edgeState.GetLast(state.Name)

		if b.heartbeatInterval > 0 {
			needsHeartbeat = !hasPrev || time.Since(lastSent) > b.heartbeatInterval
		}
	}
	if isChanged || needsHeartbeat {
		logging.Debug("Publishing device state", "deviceState", state)
		topic := "device/" + state.Name + "/state"

		err := b.PublishJSON(ctx, topic, FireAndForget, true, state)
		if err == nil {
			b.edgeState.Update(state.Name, state)
		}
		return err
	}
	return nil

}
func (b *edgeBroker) OnMessage(ctx context.Context, topic string, payload []byte) {
	logging.Debug("Received cmd message", "topic", topic)
	// Parse device name from topic
	parts := strings.Split(topic, "/")
	// uhn/<edge>/device/<deviceName>/cmd
	// uhn/<edge>/cmd
	if len(parts) == 3 && parts[2] == "cmd" {
		b.onCommand(ctx, topic, payload)
		return
	}
	if len(parts) < 5 {
		logging.Warn("cmd topic malformed", "topic", topic)
		return
	}
	b.onDeviceCommand(ctx, parts[3], payload)

}
func (b *edgeBroker) onCommand(ctx context.Context, topic string, payload []byte) {
	logging.Debug("Received cmd message", "topic", topic)
	var inCommand uhn.IncomingCommand
	if err := json.Unmarshal(payload, &inCommand); err != nil {
		logging.Warn("cmd json", "error", err)
		return
	}
	err := b.EdgeSubscriber.OnCommand(ctx, inCommand)
	if err != nil {
		logging.Warn("cmd handling", "error", err)
	}

}
func (b *edgeBroker) onDeviceCommand(ctx context.Context, deviceName string, payload []byte) {
	logging.Debug("Received device cmd message", "device", deviceName)
	var inCommand uhn.IncomingDeviceCommand
	if err := json.Unmarshal(payload, &inCommand); err != nil {
		logging.Warn("cmd json", "error", err)
		return
	}
	inCommand.Device = deviceName
	err := b.EdgeSubscriber.OnDeviceCommand(ctx, inCommand)
	if err != nil {
		logging.Warn("cmd handling", "error", err)
	}
}
func (b *edgeBroker) ClearPublishedState() {
	b.edgeState.Clear()
}
