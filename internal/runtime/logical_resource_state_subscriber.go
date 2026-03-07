package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
)

// LogicalResourceStateSubscriber subscribes to "resource/state/+" to capture
// retained logical resource state messages from the MQTT broker. It operates in two phases:
//
//   - Buffering phase (before DrainBuffered): accumulates retained messages so
//     they can be replayed on runtime startup.
//   - Flushed phase (after DrainBuffered): drops self-echoes via SignalTracker
//     since the runtime is already running and the IPCBridge handles live updates.
type LogicalResourceStateSubscriber struct {
	signalTracker *SignalTracker
	broker        messaging.Broker

	mu           sync.Mutex
	buffered     []LogicalResourceMQTTPayload
	flushed      bool
	subscription messaging.Subscription
}

// NewLogicalResourceStateSubscriber creates a new subscriber.
func NewLogicalResourceStateSubscriber(signalTracker *SignalTracker, broker messaging.Broker) *LogicalResourceStateSubscriber {
	return &LogicalResourceStateSubscriber{
		signalTracker: signalTracker,
		broker:        broker,
	}
}

// Subscribe subscribes to resource/state/+ on the broker.
func (s *LogicalResourceStateSubscriber) Subscribe(ctx context.Context) {
	sub, err := s.broker.Subscribe(ctx, "resource/state/+", messaging.AtLeastOnce, s)
	if err != nil {
		logging.Error("LogicalResourceStateSubscriber: subscribe failed", "error", err)
		return
	}
	s.mu.Lock()
	s.subscription = sub
	s.mu.Unlock()
}

// OnMessage handles an incoming MQTT message on "logical-resource/state/{resourceId}".
func (s *LogicalResourceStateSubscriber) OnMessage(ctx context.Context, topic string, payload []byte) {
	var msg LogicalResourceMQTTPayload
	if err := json.Unmarshal(payload, &msg); err != nil {
		logging.Warn("LogicalResourceStateSubscriber: invalid JSON", "topic", topic, "error", err)
		return
	}

	// Extract resourceId from topic for logging
	parts := strings.Split(topic, "/")
	resourceID := ""
	if len(parts) >= 5 {
		resourceID = parts[4]
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.flushed {
		// Buffering phase: accumulate retained messages
		logging.Debug("LogicalResourceStateSubscriber: buffered", "resourceId", resourceID, "value", msg.Value)
		s.buffered = append(s.buffered, msg)
		return
	}

	// Flushed phase: drop self-echoes
	if s.signalTracker.IsEcho(msg.ResourceID, msg.Timestamp) {
		logging.Debug("LogicalResourceStateSubscriber: skipping self-echo", "resourceId", resourceID)
		return
	}

	// After flush, any non-echo message is unexpected. Log for visibility.
	logging.Debug("LogicalResourceStateSubscriber: ignoring post-flush message", "resourceId", resourceID)
}

// DrainBuffered returns all buffered retained messages and marks the subscriber
// as flushed. Subsequent messages will be checked for self-echo and dropped.
func (s *LogicalResourceStateSubscriber) DrainBuffered() []LogicalResourceMQTTPayload {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := s.buffered
	s.buffered = nil
	s.flushed = true
	return result
}

// Resubscribe unsubscribes, clears buffer state, and re-subscribes.
// Used when the runtime restarts so retained messages are re-captured.
func (s *LogicalResourceStateSubscriber) Resubscribe(ctx context.Context) {
	s.mu.Lock()
	sub := s.subscription
	s.subscription = nil
	s.buffered = nil
	s.flushed = false
	s.mu.Unlock()

	if sub != nil {
		if err := sub.Unsubscribe(ctx); err != nil {
			logging.Warn("LogicalResourceStateSubscriber: unsubscribe failed", "error", err)
		}
	}

	s.Subscribe(ctx)
}
