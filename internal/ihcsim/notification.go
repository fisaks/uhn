package ihcsim

import (
	"sync"
	"time"

	"github.com/fisaks/uhn/internal/ihc"
)

// Notifier manages long-poll notification delivery for IHC sessions.
// Changes are buffered per-session between polls.
type Notifier struct {
	mu      sync.Mutex
	waiters map[string]chan struct{}            // sessionID => wake channel
	pending map[string][]ihc.ResourceValueEnvelope // sessionID => buffered changes
}

// NewNotifier creates a new Notifier.
func NewNotifier() *Notifier {
	return &Notifier{
		waiters: make(map[string]chan struct{}),
		pending: make(map[string][]ihc.ResourceValueEnvelope),
	}
}

// QueueForSession adds envelopes to a session's pending buffer.
// Used for initial subscription values.
func (n *Notifier) QueueForSession(sessionID string, envs []ihc.ResourceValueEnvelope) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.pending[sessionID] = append(n.pending[sessionID], envs...)
	// Wake the session if it's waiting
	if ch, ok := n.waiters[sessionID]; ok {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Notify sends a resource change to all specified sessions.
func (n *Notifier) Notify(sessionIDs []string, env ihc.ResourceValueEnvelope) {
	n.mu.Lock()
	defer n.mu.Unlock()

	for _, sid := range sessionIDs {
		// Buffer the change — dedup by replacing any existing entry for the same resource
		existing := n.pending[sid]
		found := false
		for i, e := range existing {
			if e.ResourceID == env.ResourceID {
				existing[i] = env
				found = true
				break
			}
		}
		if !found {
			n.pending[sid] = append(existing, env)
		}

		// Wake the session if it's waiting
		if ch, ok := n.waiters[sid]; ok {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

// Wait blocks until changes are available for the session or timeout expires.
// Returns the buffered changes (may be empty on timeout).
func (n *Notifier) Wait(sessionID string, timeout time.Duration) []ihc.ResourceValueEnvelope {
	// Check if there are already pending changes
	n.mu.Lock()
	if changes := n.drainLocked(sessionID); len(changes) > 0 {
		n.mu.Unlock()
		return changes
	}

	// Register a waiter
	ch := make(chan struct{}, 1)
	n.waiters[sessionID] = ch
	n.mu.Unlock()

	// Wait for notification or timeout
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ch:
	case <-timer.C:
	}

	// Drain and cleanup
	n.mu.Lock()
	delete(n.waiters, sessionID)
	changes := n.drainLocked(sessionID)
	n.mu.Unlock()

	return changes
}

// WakeAll wakes all waiting sessions (e.g., on session expiry).
func (n *Notifier) WakeAll() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, ch := range n.waiters {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// CleanupSession removes all state for a session.
func (n *Notifier) CleanupSession(sessionID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.pending, sessionID)
	delete(n.waiters, sessionID)
}

// drainLocked returns and clears all pending changes for a session.
// Caller must hold n.mu.
func (n *Notifier) drainLocked(sessionID string) []ihc.ResourceValueEnvelope {
	changes := n.pending[sessionID]
	if len(changes) > 0 {
		delete(n.pending, sessionID)
	}
	return changes
}
