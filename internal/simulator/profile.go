package simulator

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/womat/mbserver"
)

// DeviceProfile defines a simulated device behavior profile.
// Profiles can initialize device registers and update them periodically.
type DeviceProfile interface {
	// Name returns the profile identifier (matches catalog device type).
	Name() string
	// Init sets up initial register values for the device.
	Init(dev *mbserver.Device)
	// Tick updates register values (called periodically).
	Tick(dev *mbserver.Device)
}

// RegisterProfile adds a profile for a specific device.
func (s *SimStore) RegisterProfile(deviceName string, profile DeviceProfile) {
	s.profiles[deviceName] = profile
}

// ProfileTickerControl manages the profile ticker goroutine.
// Starts disabled; use Enable/Disable via REST or test code.
type ProfileTickerControl struct {
	simStore *SimStore
	interval time.Duration
	mu       sync.Mutex
	enabled  bool
	cancel   context.CancelFunc
}

// NewProfileTicker creates a ticker control (starts disabled).
func NewProfileTicker(simStore *SimStore, interval time.Duration) *ProfileTickerControl {
	return &ProfileTickerControl{
		simStore: simStore,
		interval: interval,
	}
}

// Enable starts the ticker goroutine. No-op if already enabled.
func (c *ProfileTickerControl) Enable() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.enabled {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.enabled = true

	go func() {
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.tickAll()
			}
		}
	}()
	log.Printf("Profile ticker enabled (interval=%s)", c.interval)
}

// Disable stops the ticker goroutine. No-op if already disabled.
func (c *ProfileTickerControl) Disable() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		return
	}
	c.cancel()
	c.cancel = nil
	c.enabled = false
	log.Printf("Profile ticker disabled")
}

// IsEnabled returns whether the ticker is currently running.
func (c *ProfileTickerControl) IsEnabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

func (c *ProfileTickerControl) tickAll() {
	for deviceName, profile := range c.simStore.profiles {
		deviceCfg := c.simStore.GetDeviceConfig(deviceName)
		if deviceCfg == nil {
			continue
		}
		srv := c.simStore.GetServer(deviceCfg.BusId)
		if srv == nil {
			continue
		}
		if dev, ok := srv.Devices[deviceCfg.UnitID]; ok {
			profile.Tick(&dev)
		}
	}
}
