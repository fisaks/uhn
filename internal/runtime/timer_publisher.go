package runtime

import (
	"context"
	"encoding/json"

	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
)

// TimerPublisher publishes timer state to MQTT.
// Topic: timer/state/{resourceId} (broker auto-prefixes with uhn/{edge}/).
type TimerPublisher struct {
	broker  messaging.Broker
	tracker *SignalTracker
}

// NewTimerPublisher creates a new TimerPublisher.
func NewTimerPublisher(broker messaging.Broker, tracker *SignalTracker) *TimerPublisher {
	return &TimerPublisher{
		broker:  broker,
		tracker: tracker,
	}
}

// Publish publishes timer state to MQTT.
func (p *TimerPublisher) Publish(ctx context.Context, state TimerRuntimeState, timestamp int64) {
	topic := "timer/state/" + state.ID
	payload := TimerMQTTPayload{
		ResourceID: state.ID,
		Active:     state.Active,
		StartedAt:  state.StartedAt,
		StopAt:     state.StopAt,
		Timestamp:  timestamp,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		logging.Error("TimerPublisher: marshal failed", "resourceId", state.ID, "error", err)
		return
	}

	// Record in tracker before publishing (for self-echo prevention)
	p.tracker.RecordPublish(state.ID, timestamp)

	if err := p.broker.Publish(ctx, topic, messaging.AtLeastOnce, true, data); err != nil {
		logging.Error("TimerPublisher: MQTT publish failed", "resourceId", state.ID, "error", err)
	} else {
		logging.Debug("TimerPublisher: published", "resourceId", state.ID, "active", state.Active)
	}
}
