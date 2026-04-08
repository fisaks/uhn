package ihc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/uhn"
	"github.com/fisaks/uhn/internal/util"
)

// IHCDriver manages the lifecycle of a single IHC controller connection.
// It handles authentication, notification subscriptions, the long-poll
// notification loop, health-check canary, and command dispatch.
//
// Supports two modes:
//   - Static: resources from config (legacy, when cfg.Resources is non-empty)
//   - Dynamic: resources from ResourceMap (when a ResourceMapDeviceProvider is set)
//
// In dynamic mode, the driver authenticates immediately but waits for
// OnResourceMapReady() before subscribing to IHC notifications. On blueprint
// reload, it disconnects and re-subscribes with the new resource set.
type IHCDriver struct {
	cfg    *config.IHCControllerConfig
	client *SOAPClient
	state  uhn.StateUpdater

	// Resource type lookup: resource ID -> UHN type string
	resourceTypes map[int]string

	// All resource IDs to subscribe for notifications
	allResourceIDs []int

	// Dynamic resource provider (nil = use static config)
	resourceMapProvider uhn.ResourceMapDeviceProvider

	// Availability publisher (optional — publishes device online/offline to MQTT)
	availPublisher uhn.AvailabilityPublisher

	// Health-check canary
	healthCheckIDs   []int
	healthCheckSec   int
	healthPending    map[int]bool // IDs we flipped, waiting for notification
	healthPendingMu  sync.Mutex
	healthLastValues map[int]bool // canary resource IDs → last known value (flipped to determine next write value)
	healthFailCount  int          // consecutive health-check failures
	healthMaxFails   int          // failures before forcing reconnect

	// Notification watchdog
	watchdogTimeout time.Duration

	// Lifecycle management
	cancel       context.CancelFunc
	done         chan struct{}
	resourceCh   chan struct{} // signalled when ResourceMap changes
	resourceOnce sync.Once    // ensures resourceCh is created once
}

// NewIHCDriver creates a driver for one IHC controller.
func NewIHCDriver(cfg *config.IHCControllerConfig, state uhn.StateUpdater) *IHCDriver {
	d := &IHCDriver{
		cfg:             cfg,
		client:          NewSOAPClient(cfg.Host, cfg.Port),
		state:           state,
		resourceTypes:   make(map[int]string),
		healthPending:   make(map[int]bool),
		watchdogTimeout: time.Duration(cfg.WaitTimeoutSec*3) * time.Second,
		done:            make(chan struct{}),
		resourceCh:      make(chan struct{}, 1),
	}

	// Populate from static config if present
	d.loadStaticResources()

	// Health-check config
	if cfg.HealthCheck != nil {
		d.healthCheckSec = cfg.HealthCheck.IntervalSec
		d.healthMaxFails = cfg.HealthCheck.MaxFailures
	}
	d.healthCheckIDs = cfg.HealthCheckResourceIDs
	d.healthLastValues = makeIDSet(cfg.HealthCheckResourceIDs)

	return d
}

// SetResourceMapProvider enables dynamic mode: resources come from the ResourceMap
// instead of (or in addition to) static config. Call before Start().
func (d *IHCDriver) SetResourceMapProvider(provider uhn.ResourceMapDeviceProvider) {
	d.resourceMapProvider = provider
}

// SetAvailabilityPublisher sets the publisher used to report device online/offline status.
func (d *IHCDriver) SetAvailabilityPublisher(publisher uhn.AvailabilityPublisher) {
	d.availPublisher = publisher
}

func (d *IHCDriver) publishAvailability(ctx context.Context, online bool) {
	if d.availPublisher != nil {
		d.availPublisher.PublishDeviceAvailability(ctx, d.cfg.Name, online)
	}
}

// OnResourceMapReady is called when the blueprint's ResourceMap becomes available
// or changes (reload). It triggers a reconnect with the new resource set.
func (d *IHCDriver) OnResourceMapReady(ctx context.Context) {
	if d.resourceMapProvider == nil {
		return
	}

	resources, ok := d.resourceMapProvider.DeviceResourcesFromMap(d.cfg.Name)
	if !ok || len(resources) == 0 {
		logging.Info("IHC ResourceMap has no resources for this controller",
			"controller", d.cfg.Name)
		return
	}

	d.updateResources(resources)
	logging.Info("IHC resources updated from ResourceMap",
		"controller", d.cfg.Name,
		"resources", len(resources))

	// Signal the notification loop to reconnect with new resources
	select {
	case d.resourceCh <- struct{}{}:
	default:
	}
}

// updateResources replaces the resource type map and ID list from a ResourceMap result.
func (d *IHCDriver) updateResources(resources map[int]string) {
	resourceTypes := make(map[int]string, len(resources))
	allIDs := make([]int, 0, len(resources))
	for pin, resType := range resources {
		resourceTypes[pin] = resType
		allIDs = append(allIDs, pin)
	}

	// Include health-check IDs not already in the resource list
	for _, hcID := range d.healthCheckIDs {
		if _, exists := resourceTypes[hcID]; !exists {
			allIDs = append(allIDs, hcID)
		}
	}

	d.resourceTypes = resourceTypes
	d.allResourceIDs = allIDs
}

// loadStaticResources populates from cfg.Resources (legacy mode).
func (d *IHCDriver) loadStaticResources() {
	length := len(d.cfg.Resources)
	if length == 0 {
		return
	}
	resourceTypes := make(map[int]string, length)
	allIDs := make([]int, 0, length)
	for _, res := range d.cfg.Resources {
		resourceTypes[res.ResourceIntID] = res.Type
		allIDs = append(allIDs, res.ResourceIntID)
	}
	// Include health-check resource IDs in subscription
	for _, hcID := range d.cfg.HealthCheckResourceIDs {
		if _, exists := resourceTypes[hcID]; !exists {
			allIDs = append(allIDs, hcID)
		}
	}
	d.resourceTypes = resourceTypes
	d.allResourceIDs = allIDs
}

// Start begins the driver's lifecycle: authenticate, subscribe, and run the
// notification loop. Blocks until ctx is cancelled or the driver is stopped.
// Uses exponential backoff (5s → 10s → 20s → … → 60s cap) on reconnect failures.
func (d *IHCDriver) Start(ctx context.Context) {
	ctx, d.cancel = context.WithCancel(ctx)
	defer close(d.done)

	// In dynamic mode with no static resources, wait for ResourceMap first
	if len(d.allResourceIDs) == 0 && d.resourceMapProvider != nil {
		logging.Info("IHC waiting for ResourceMap before connecting",
			"controller", d.cfg.Name)
		select {
		case <-ctx.Done():
			return
		case <-d.resourceCh:
			// ResourceMap is ready, proceed
		}
	}

	backoff := 5 * time.Second
	const maxBackoff = 60 * time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		if len(d.allResourceIDs) == 0 {
			// No resources to subscribe to — wait for ResourceMap signal
			select {
			case <-ctx.Done():
				return
			case <-d.resourceCh:
				continue
			}
		}

		if err := d.connectAndRun(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			logging.Error("IHC driver error, retrying",
				"controller", d.cfg.Name, "error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return
			case <-d.resourceCh:
				// ResourceMap changed — reconnect immediately with new resources
				backoff = 5 * time.Second
				continue
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxBackoff)
		} else {
			backoff = 5 * time.Second
		}
	}
}

// Stop signals the driver to shut down and waits for it to finish.
func (d *IHCDriver) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	<-d.done
	_ = d.client.Disconnect(context.Background())
}

// connectAndRun performs a full connection cycle: auth → enable → notification loop.
// Returns on unrecoverable error or context cancellation.
func (d *IHCDriver) connectAndRun(ctx context.Context) error {
	// Authenticate
	logging.Info("IHC authenticating", "controller", d.cfg.Name, "host", d.cfg.Host)
	ok, err := d.client.Authenticate(ctx, d.cfg.Username, d.cfg.Password)
	if err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}
	if !ok {
		return fmt.Errorf("authentication rejected by controller")
	}
	logging.Info("IHC authenticated", "controller", d.cfg.Name)

	// Enable notifications
	if err := d.client.EnableRuntimeValueNotifications(ctx, d.allResourceIDs); err != nil {
		return fmt.Errorf("enable notifications: %w", err)
	}
	logging.Info("IHC notifications enabled",
		"controller", d.cfg.Name,
		"resources", len(d.allResourceIDs))

	d.publishAvailability(ctx, true)

	// Run notification loop
	err = d.notificationLoop(ctx)
	d.publishAvailability(ctx, false)
	return err
}

// notificationLoop runs the long-poll wait loop and processes incoming values.
func (d *IHCDriver) notificationLoop(ctx context.Context) error {
	consecutiveErrors := 0
	//Guards against the long-poll WaitForResourceValueChanges hanging indefinitely.
	watchdogTimer := time.NewTimer(d.watchdogTimeout)
	defer watchdogTimer.Stop()

	// Health-check ticker — started after the first successful long-poll so
	// that healthLastValues is populated with real canary state before the
	// first check runs (avoids false positive if maxFailures is 1).
	healthEnabled := d.healthCheckSec > 0 && len(d.healthCheckIDs) > 0
	var healthTicker *time.Ticker

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-d.resourceCh:
			// ResourceMap changed — disconnect and let caller reconnect
			logging.Info("IHC ResourceMap changed, reconnecting with new resources",
				"controller", d.cfg.Name)
			_ = d.client.Disconnect(ctx)
			return nil // clean return triggers reconnect in Start()
		default:
		}

		// Check health-check ticker
		if healthTicker != nil {
			select {
			case <-healthTicker.C:
				if d.performHealthCheck(ctx) {
					return fmt.Errorf("health-check failed: canary notifications not received")
				}
			default:
			}
		}

		// Check watchdog
		select {
		case <-watchdogTimer.C:
			logging.Warn("IHC watchdog triggered — no response within timeout, forcing reconnect",
				"controller", d.cfg.Name)
			return fmt.Errorf("watchdog timeout")
		default:
		}

		// Long-poll for changes
		envelopes, err := d.client.WaitForResourceValueChanges(ctx, d.cfg.WaitTimeoutSec)
		if err != nil {
			if errors.Is(err, ErrSessionExpired) {
				logging.Warn("IHC session expired, re-authenticating", "controller", d.cfg.Name)
				if err := d.reconnect(ctx); err != nil {
					return err
				}
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			consecutiveErrors++
			logging.Error("IHC wait error",
				"controller", d.cfg.Name,
				"error", err,
				"consecutive", consecutiveErrors)
			if consecutiveErrors >= d.cfg.MaxConsecutiveErrors {
				return fmt.Errorf("too many consecutive errors (%d)", consecutiveErrors)
			}
			time.Sleep(time.Duration(consecutiveErrors) * time.Second)
			continue
		}

		// Reset watchdog and error counter on any successful response
		watchdogTimer.Reset(d.watchdogTimeout)
		consecutiveErrors = 0

		// Start health-check ticker after first successful long-poll return
		// (even if empty — the initial poll delivers canary state on subscribe)
		if healthEnabled && healthTicker == nil {
			healthTicker = time.NewTicker(time.Duration(d.healthCheckSec) * time.Second)
			defer healthTicker.Stop()
		}

		// Process envelopes
		logging.Debug("IHC wait response",
			"controller", d.cfg.Name,
			"envelopes", len(envelopes))
		now := time.Now().UnixMilli()
		for _, env := range envelopes {
			d.processEnvelope(ctx, env, now)
		}
	}
}

// processEnvelope handles a single resource value notification.
func (d *IHCDriver) processEnvelope(ctx context.Context, env ResourceValueEnvelope, timestamp int64) {
	// Track latest value for health-check canary resources (used to flip reliably)
	if _, isCanary := d.healthLastValues[env.ResourceID]; isCanary {
		if b, ok := env.Value.ToAny().(bool); ok {
			d.healthPendingMu.Lock()
			d.healthLastValues[env.ResourceID] = b
			d.healthPendingMu.Unlock()
		}
	}

	// Clear health-check pending flag if this is a canary response
	d.healthPendingMu.Lock()
	if d.healthPending[env.ResourceID] {
		delete(d.healthPending, env.ResourceID)
		d.healthPendingMu.Unlock()
		logging.Debug("IHC health-check response received",
			"controller", d.cfg.Name,
			"resource", config.FormatHexID(env.ResourceID))
		// Fall through to normal processing — canary may also be a regular resource
	} else {
		d.healthPendingMu.Unlock()
	}

	// Lookup resource type from config
	resType, ok := d.resourceTypes[env.ResourceID]
	if !ok {
		logging.Debug("IHC notification for unconfigured resource",
			"controller", d.cfg.Name,
			"resource", config.FormatHexID(env.ResourceID))
		return
	}

	value := env.Value.ToAny()
	if value == nil {
		return
	}

	logging.Debug("IHC notification",
		"controller", d.cfg.Name,
		"resource", config.FormatHexID(env.ResourceID),
		"type", resType,
		"value", value)

	d.state.UpdatePhysicalStateByAddress(ctx, d.cfg.Name, resType, env.ResourceID, value, timestamp)
}

// reconnect performs a full re-authentication and re-enables notifications.
// Returns nil on success so the caller can continue the notification loop.
func (d *IHCDriver) reconnect(ctx context.Context) error {
	logging.Info("IHC reconnecting", "controller", d.cfg.Name)

	ok, err := d.client.Authenticate(ctx, d.cfg.Username, d.cfg.Password)
	if err != nil {
		return fmt.Errorf("re-authenticate: %w", err)
	}
	if !ok {
		return fmt.Errorf("re-authentication rejected by controller")
	}

	if err := d.client.EnableRuntimeValueNotifications(ctx, d.allResourceIDs); err != nil {
		return fmt.Errorf("re-enable notifications: %w", err)
	}

	logging.Info("IHC reconnected", "controller", d.cfg.Name)
	return nil
}

// performHealthCheck flips health-check resources to verify the notification
// pipeline is alive. The flipped values should come back through the next
// WaitForResourceValueChanges response. Returns true if the pipeline is dead
// and a reconnect should be forced.
func (d *IHCDriver) performHealthCheck(ctx context.Context) bool {
	d.healthPendingMu.Lock()
	// Check if previous health-check responses never came back
	if len(d.healthPending) > 0 {
		d.healthFailCount++
		if d.healthMaxFails > 0 && d.healthFailCount >= d.healthMaxFails {
			d.healthPending = make(map[int]bool)
			d.healthPendingMu.Unlock()
			logging.Warn("IHC health-check failed: canary notifications not received, forcing reconnect",
				"controller", d.cfg.Name,
				"consecutiveFailures", d.healthFailCount)
			d.healthFailCount = 0
			return true
		}
		d.healthPendingMu.Unlock()
		logging.Warn("IHC health-check: canary notifications not received",
			"controller", d.cfg.Name,
			"consecutiveFailures", d.healthFailCount)
		return false
	}
	d.healthFailCount = 0

	for _, id := range d.healthCheckIDs {
		d.healthPending[id] = true
	}
	d.healthPendingMu.Unlock()

	for _, id := range d.healthCheckIDs {
		flipped := !d.healthLastValues[id]
		_, err := d.client.SetResourceValue(ctx, id, BoolValue(flipped))
		if err != nil {
			logging.Error("IHC health-check: failed to flip canary resource",
				"controller", d.cfg.Name,
				"resource", config.FormatHexID(id),
				"error", err)
		}
	}
	return false
}

// --- DeviceDriver interface implementation ---

// HandleSignal forwards a signal (e.g., emitSignal for a writable IHC input)
// to the IHC controller via setResourceValue. The controller's function blocks
// react to the input change and the resulting state flows back through notifications.
func (d *IHCDriver) HandleSignal(ctx context.Context, pin any, value any) error {
	resourceID, ok := util.ToIntOk(pin)
	if !ok {
		return fmt.Errorf("IHC HandleSignal: expected numeric pin, got %T", pin)
	}
	ihcValue, err := anyToIHCValue(resourceID, value, d.resourceTypes)
	if err != nil {
		return err
	}
	return d.withReauth(ctx, func() error {
		_, err := d.client.SetResourceValue(ctx, resourceID, ihcValue)
		return err
	})
}

// SetOutput writes an output value (relay, dimmer) to the IHC controller.
func (d *IHCDriver) SetOutput(ctx context.Context, pin any, value any) error {
	resourceID, ok := util.ToIntOk(pin)
	if !ok {
		return fmt.Errorf("IHC SetOutput: expected numeric pin, got %T", pin)
	}
	ihcValue, err := anyToIHCValue(resourceID, value, d.resourceTypes)
	if err != nil {
		return err
	}
	return d.withReauth(ctx, func() error {
		_, err := d.client.SetResourceValue(ctx, resourceID, ihcValue)
		return err
	})
}

// withReauth executes fn and retries once after a full reconnect (reauth +
// re-enable notifications) if the session has expired. This ensures commands
// don't silently fail when the IHC session times out, and that the long-poll
// notification subscription is restored on the new session.
func (d *IHCDriver) withReauth(ctx context.Context, fn func() error) error {
	err := fn()
	if err == nil || !errors.Is(err, ErrSessionExpired) {
		return err
	}
	logging.Warn("IHC session expired during command, reconnecting",
		"controller", d.cfg.Name)
	if reconnErr := d.reconnect(ctx); reconnErr != nil {
		return fmt.Errorf("reauth after session expiry: %w", reconnErr)
	}
	return fn()
}

// BypassSignalState returns true — IHC signals are forwarded to the controller,
// not stored locally. State flows back as physical state (P) via notifications.
func (d *IHCDriver) BypassSignalState() bool { return true }

// anyToIHCValue converts a Go value to the appropriate IHCValue based on resource type.
func anyToIHCValue(resourceID int, value any, resourceTypes map[int]string) (IHCValue, error) {
	resType := resourceTypes[resourceID]
	switch resType {
	case "digitalOutput", "digitalInput":
		switch v := value.(type) {
		case bool:
			return BoolValue(v), nil
		case float64:
			return BoolValue(v != 0), nil
		case int:
			return BoolValue(v != 0), nil
		default:
			return IHCValue{}, fmt.Errorf("IHC resource 0x%X (%s): expected bool, got %T", resourceID, resType, value)
		}
	case "analogOutput":
		switch v := value.(type) {
		case float64:
			if v == float64(int(v)) { // no fractional part, treat as int
				return IntValue(int(v)), nil
			}
			return FloatValue(v), nil
		case int:
			return IntValue(v), nil
		default:
			return IHCValue{}, fmt.Errorf("IHC resource 0x%X (%s): expected number, got %T", resourceID, resType, value)
		}
	case "analogInput":
		return IHCValue{}, fmt.Errorf("IHC resource 0x%X (%s): read-only, cannot write", resourceID, resType)
	default:
		return IHCValue{}, fmt.Errorf("IHC resource 0x%X: unknown type %q", resourceID, resType)
	}
}

func makeIDSet(ids []int) map[int]bool {
	m := make(map[int]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}
