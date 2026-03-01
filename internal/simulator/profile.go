package simulator

import (
	"context"
	"log"
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

// StartProfileTicker starts a goroutine that calls Tick() on all registered
// profiles every interval. Stops when ctx is cancelled.
func (s *SimStore) StartProfileTicker(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for deviceName, profile := range s.profiles {
					deviceCfg := s.GetDeviceConfig(deviceName)
					if deviceCfg == nil {
						continue
					}
					srv := s.GetServer(deviceCfg.BusId)
					if srv == nil {
						continue
					}
					if dev, ok := srv.Devices[deviceCfg.UnitID]; ok {
						profile.Tick(&dev)
					}
				}
			}
		}
	}()
	log.Printf("Profile ticker started (interval=%s)", interval)
}
