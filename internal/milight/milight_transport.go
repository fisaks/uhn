package milight

import (
	"context"
	"fmt"
	"time"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/uhn"
)

// sideEffect is an additional state update published after a command succeeds.
type sideEffect struct {
	resType string
	pin     int
	value   any
}

// transportCommand is a command queued for the transport to send via UDP.
type transportCommand struct {
	cmd         [9]byte
	zone        byte
	device      string
	resType     string
	pin         int
	value       any
	sideEffects []sideEffect // additional state updates after command succeeds
}

// MilightTransport manages the iBox2 UDP connection and serializes commands
// across all zones on the bridge. One transport per iBox2.
// Implements uhn.DeviceTransport.
type MilightTransport struct {
	cfg    *config.MilightConfig
	client *UDPClient
	state  uhn.StateUpdater

	cmdCh  chan transportCommand
	cancel context.CancelFunc
	done   chan struct{}
}

// NewMilightTransport creates a transport for one iBox2.
func NewMilightTransport(cfg *config.MilightConfig, state uhn.StateUpdater) *MilightTransport {
	return &MilightTransport{
		cfg:    cfg,
		client: NewUDPClient(cfg.Host, cfg.Port),
		state:  state,
		cmdCh:  make(chan transportCommand, 64),
		done:   make(chan struct{}),
	}
}

// Start begins the transport lifecycle. Blocks until ctx is cancelled.
func (t *MilightTransport) Start(ctx context.Context) {
	ctx, t.cancel = context.WithCancel(ctx)
	defer close(t.done)

	if err := t.runLoop(ctx); err != nil && ctx.Err() == nil {
		logging.Error("Mi-Light transport stopped", "host", t.cfg.Host, "error", err)
	}
}

// Stop signals the transport to shut down and waits for completion.
func (t *MilightTransport) Stop() {
	if t.cancel != nil {
		t.cancel()
	}
	<-t.done
	t.client.Close()
}

// runLoop processes the command queue. Each command gets a fresh handshake.
func (t *MilightTransport) runLoop(ctx context.Context) error {
	cmdDelay := time.Duration(t.cfg.CommandDelayMs) * time.Millisecond
	var lastSend time.Time

	logging.Info("Mi-Light transport ready", "host", t.cfg.Host, "port", t.cfg.Port)

	// Drain any stale commands from the channel before starting
	t.drainCmdChannel()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case cmd := <-t.cmdCh:
			// Enforce minimum delay between sends
			if elapsed := time.Since(lastSend); elapsed < cmdDelay {
				time.Sleep(cmdDelay - elapsed)
			}

			if err := t.sendWithFreshSession(cmd); err != nil {
				logging.Warn("Mi-Light command failed, dropping",
					"host", t.cfg.Host, "device", cmd.device, "pin", cmd.pin, "error", err)
				lastSend = time.Now()
				continue
			}
			lastSend = time.Now()

			// Publish assumed state
			now := time.Now().UnixMilli()
			t.state.UpdatePhysicalStateByAddress(ctx, cmd.device, cmd.resType, cmd.pin, cmd.value, now)
			for _, se := range cmd.sideEffects {
				t.state.UpdatePhysicalStateByAddress(ctx, cmd.device, se.resType, se.pin, se.value, now)
			}
		}
	}
}

// sendWithFreshSession opens a connection, handshakes, sends the command, and closes.
// On no ACK, reconnects and retries up to cfg.CommandRetries times.
func (t *MilightTransport) sendWithFreshSession(cmd transportCommand) error {
	err := t.connectAndSend(cmd)
	for retry := 0; err == ErrNoACK && retry < t.cfg.CommandRetries; retry++ {
		logging.Warn("Mi-Light no ACK, retrying",
			"host", t.cfg.Host, "device", cmd.device, "attempt", retry+2)
		err = t.connectAndSend(cmd)
	}
	return err
}

func (t *MilightTransport) connectAndSend(cmd transportCommand) error {
	ackTimeout := time.Duration(t.cfg.CommandTimeoutMs) * time.Millisecond
	t.client.Close()
	if err := t.client.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	if err := t.client.Handshake(ackTimeout); err != nil {
		t.client.Close()
		return fmt.Errorf("handshake: %w", err)
	}
	err := t.client.SendCommand(cmd.cmd, cmd.zone, ackTimeout)
	t.client.Close()
	return err
}

// enqueue sends a command to the transport's command queue.
// Returns an error if the queue is full (non-blocking).
func (t *MilightTransport) enqueue(cmd transportCommand) error {
	select {
	case t.cmdCh <- cmd:
		return nil
	default:
		return fmt.Errorf("Mi-Light command queue full for %s", t.cfg.Host)
	}
}

// drainCmdChannel discards any queued commands (e.g. after reconnect).
func (t *MilightTransport) drainCmdChannel() {
	for {
		select {
		case <-t.cmdCh:
			// discard
		default:
			return
		}
	}
}
