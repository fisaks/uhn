package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
)

// TimerStateSubscriber subscribes to "timer/state/+" to capture retained timer
// state messages from the MQTT broker. It operates in two phases:
//
//   - Buffering phase (before DrainBuffered): accumulates retained messages so
//     they can be replayed on runtime startup.
//   - Flushed phase (after DrainBuffered): drops self-echoes via SignalTracker
//     since the runtime is already running and the IPCBridge handles live updates.
type TimerStateSubscriber struct {
	signalTracker *SignalTracker
	broker        messaging.Broker

	mu           sync.Mutex
	buffered     []TimerMQTTPayload
	flushed      bool
	subscription messaging.Subscription
}

// NewTimerStateSubscriber creates a new subscriber.
func NewTimerStateSubscriber(signalTracker *SignalTracker, broker messaging.Broker) *TimerStateSubscriber {
	return &TimerStateSubscriber{
		signalTracker: signalTracker,
		broker:        broker,
	}
}

// Subscribe subscribes to timer/state/+ on the broker.
func (s *TimerStateSubscriber) Subscribe(ctx context.Context) {
	sub, err := s.broker.Subscribe(ctx, "timer/state/+", messaging.AtLeastOnce, s)
	if err != nil {
		logging.Error("TimerStateSubscriber: subscribe failed", "error", err)
		return
	}
	s.mu.Lock()
	s.subscription = sub
	s.mu.Unlock()
}

// OnMessage handles an incoming MQTT message on "timer/state/{resourceId}".
func (s *TimerStateSubscriber) OnMessage(ctx context.Context, topic string, payload []byte) {
	var msg TimerMQTTPayload
	if err := json.Unmarshal(payload, &msg); err != nil {
		logging.Warn("TimerStateSubscriber: invalid JSON", "topic", topic, "error", err)
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
		logging.Debug("TimerStateSubscriber: buffered", "resourceId", resourceID, "active", msg.Active)
		s.buffered = append(s.buffered, msg)
		return
	}

	// Flushed phase: drop self-echoes
	if s.signalTracker.IsEcho(msg.ResourceID, msg.Timestamp) {
		logging.Debug("TimerStateSubscriber: skipping self-echo", "resourceId", resourceID)
		return
	}

	// After flush, any non-echo message is unexpected (master-originated timer state
	// updates go through timer/cmd, not timer/state). Log for visibility.
	logging.Debug("TimerStateSubscriber: ignoring post-flush message", "resourceId", resourceID)
}

// DrainBuffered returns all buffered retained messages and marks the subscriber
// as flushed. Subsequent messages will be checked for self-echo and dropped.
func (s *TimerStateSubscriber) DrainBuffered() []TimerMQTTPayload {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := s.buffered
	s.buffered = nil
	s.flushed = true
	return result
}

// Resubscribe unsubscribes, clears buffer state, and re-subscribes.
// Used when the runtime restarts so retained messages are re-captured.
func (s *TimerStateSubscriber) Resubscribe(ctx context.Context) {
	s.mu.Lock()
	sub := s.subscription
	s.subscription = nil
	s.buffered = nil
	s.flushed = false
	s.mu.Unlock()

	if sub != nil {
		if err := sub.Unsubscribe(ctx); err != nil {
			logging.Warn("TimerStateSubscriber: unsubscribe failed", "error", err)
		}
	}

	s.Subscribe(ctx)
}
