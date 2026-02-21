package runtime

import (
	"sync"
	"time"
)

const signalTrackerTTL = 5 * time.Second

type publishEntry struct {
	timestamp int64
	expiresAt time.Time
}

// SignalTracker prevents self-echo when the edge publishes a signal and
// receives it back via MQTT subscription.
type SignalTracker struct {
	mu      sync.Mutex
	entries map[string][]publishEntry
}

// NewSignalTracker creates a new SignalTracker.
func NewSignalTracker() *SignalTracker {
	return &SignalTracker{
		entries: make(map[string][]publishEntry),
	}
}

// RecordPublish records that we are about to publish a signal for a resource.
// Call this BEFORE the MQTT publish.
func (t *SignalTracker) RecordPublish(resourceID string, timestamp int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.cleanupLocked(resourceID)
	t.entries[resourceID] = append(t.entries[resourceID], publishEntry{
		timestamp: timestamp,
		expiresAt: time.Now().Add(signalTrackerTTL),
	})
}

// IsEcho returns true if the incoming signal matches a recently published one
// (same resourceID and timestamp). If matched, the entry is removed.
func (t *SignalTracker) IsEcho(resourceID string, timestamp int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	entries := t.entries[resourceID]
	for i, e := range entries {
		if e.timestamp == timestamp {
			// Remove matched entry
			t.entries[resourceID] = append(entries[:i], entries[i+1:]...)
			if len(t.entries[resourceID]) == 0 {
				delete(t.entries, resourceID)
			}
			return true
		}
	}

	t.cleanupLocked(resourceID)
	return false
}

// cleanupLocked removes expired entries for a resource. Must be called with mu held.
func (t *SignalTracker) cleanupLocked(resourceID string) {
	entries := t.entries[resourceID]
	now := time.Now()
	n := 0
	for _, e := range entries {
		if now.Before(e.expiresAt) {
			entries[n] = e
			n++
		}
	}
	if n == 0 {
		delete(t.entries, resourceID)
	} else {
		t.entries[resourceID] = entries[:n]
	}
}
