package runtime

import (
	"context"
	"encoding/json"

	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
)

// LogicalResourceStatePublisher publishes logical resource state to MQTT.
// Topic: resource/state/{resourceId} (broker auto-prefixes with uhn/{edge}/).
type LogicalResourceStatePublisher struct {
	broker  messaging.Broker
	tracker *SignalTracker
}

// NewLogicalResourceStatePublisher creates a new LogicalResourceStatePublisher.
func NewLogicalResourceStatePublisher(broker messaging.Broker, tracker *SignalTracker) *LogicalResourceStatePublisher {
	return &LogicalResourceStatePublisher{
		broker:  broker,
		tracker: tracker,
	}
}

// Publish publishes logical resource state to MQTT.
func (p *LogicalResourceStatePublisher) Publish(ctx context.Context, state LogicalResourceStateChangedPayload, timestamp int64) {
	topic := "resource/state/" + state.ResourceID
	payload := LogicalResourceMQTTPayload{
		ResourceID: state.ResourceID,
		Value:      state.Value,
		Timestamp:  timestamp,
		Details:    state.Details,
		Silent:     state.Silent,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		logging.Error("LogicalResourceStatePublisher: marshal failed", "resourceId", state.ResourceID, "error", err)
		return
	}

	// Record in tracker before publishing (for self-echo prevention)
	p.tracker.RecordPublish(state.ResourceID, timestamp)

	if err := p.broker.Publish(ctx, topic, messaging.AtLeastOnce, true, data); err != nil {
		logging.Error("LogicalResourceStatePublisher: MQTT publish failed", "resourceId", state.ResourceID, "error", err)
	} else {
		logging.Debug("LogicalResourceStatePublisher: published", "resourceId", state.ResourceID, "value", state.Value)
	}
}
