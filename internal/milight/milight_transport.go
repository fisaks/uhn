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

// gatekeeperEntry holds the address of the gatekeeper resource for one Mi-Light zone.
type gatekeeperEntry struct {
	device  string
	resType string
	pin     int
}

// MilightTransport manages the iBox2 UDP connection and serializes commands
// across all zones on the bridge. One transport per iBox2.
// Implements uhn.DeviceTransport.
type MilightTransport struct {
	cfg         *config.MilightConfig
	client      *UDPClient
	state       uhn.StateUpdater
	stateReader uhn.PhysicalStateReader

	// gatekeepers maps zone number → gatekeeper entry (only for zones with gatekeeper config).
	// Written once at construction, read in runLoop (single goroutine) — no mutex needed.
	gatekeepers map[byte]*gatekeeperEntry

	cmdCh  chan transportCommand
	cancel context.CancelFunc
	done   chan struct{}
}

// NewMilightTransport creates a transport for one iBox2.
func NewMilightTransport(cfg *config.MilightConfig, state uhn.StateUpdater, stateReader uhn.PhysicalStateReader) *MilightTransport {
	gk := make(map[byte]*gatekeeperEntry)
	for _, zone := range cfg.Zones {
		if zone.Gatekeeper != nil {
			gk[zone.Zone] = &gatekeeperEntry{
				device:  zone.Gatekeeper.Device,
				resType: zone.Gatekeeper.Type,
				pin:     zone.Gatekeeper.PinInt,
			}
		}
	}
	return &MilightTransport{
		cfg:         cfg,
		client:      NewUDPClient(cfg.Host, cfg.Port),
		state:       state,
		stateReader: stateReader,
		gatekeepers: gk,
		cmdCh:       make(chan transportCommand, 64),
		done:        make(chan struct{}),
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
			// Check gatekeeper BEFORE rate-limit sleep — dropped commands skip the delay
			if !t.checkGatekeeper(cmd) {
				t.reconfirmState(ctx, cmd)
				continue
			}

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

// reconfirmState re-publishes the current physical state for a dropped command's
// pin with a fresh timestamp. This ensures the edge local state is refreshed and
// (via MQTT) tells the master the old value is still current — allowing the UI's
// optimistic update to snap back to the real value.
func (t *MilightTransport) reconfirmState(ctx context.Context, cmd transportCommand) {
	val, found := t.stateReader.ReadPhysicalStateByAddress(cmd.device, cmd.resType, cmd.pin)
	if !found {
		return // no prior state to reconfirm
	}
	now := time.Now().UnixMilli()
	t.state.UpdatePhysicalStateByAddress(ctx, cmd.device, cmd.resType, cmd.pin, val, now)
}

// checkGatekeeper returns true if the command should proceed, false if suppressed.
// Called in runLoop before rate-limit sleep so dropped commands don't incur delay.
func (t *MilightTransport) checkGatekeeper(cmd transportCommand) bool {
	entry, ok := t.gatekeepers[cmd.zone]
	if !ok {
		return true // no gatekeeper for this zone
	}

	val, found := t.stateReader.ReadPhysicalStateByAddress(entry.device, entry.resType, entry.pin)
	if !found {
		return true // unknown state = allow (default ON)
	}

	isOn, ok := toBool(val)
	if !ok {
		return true // non-bool state = allow
	}

	if !isOn {
		logging.Debug("Mi-Light gatekeeper OFF, dropping command",
			"host", t.cfg.Host, "zone", cmd.zone, "device", cmd.device,
			"gatekeeper", entry.device)
		return false
	}

	return true
}
