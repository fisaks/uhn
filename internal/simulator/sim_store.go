package simulator

import (
	"fmt"
	"log"

	"github.com/womat/mbserver"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/simulator/profiles"
)

// SimDeviceConfig holds the simulator-side metadata for one device.
type SimDeviceConfig struct {
	Name                string
	BusId               string
	UnitID              uint8
	DigitalOutputs      uint16
	DigitalInputs       uint16
	AnalogOutputs       uint16
	AnalogInputs        uint16
	DigitalOutputsStart uint16
	DigitalInputsStart  uint16
	AnalogOutputsStart  uint16
	AnalogInputsStart   uint16
}

// SimStore is the central state for all simulated buses and devices.
// All maps are populated once during SetupFromConfig and then read-only.
type SimStore struct {
	servers       map[string]*mbserver.Server // busId => server
	deviceConfigs map[string]*SimDeviceConfig // deviceName => config
	profiles      map[string]DeviceProfile    // deviceName => profile
}

// SetupFromConfig creates a SimStore with one mbserver.Server per bus.
func SetupFromConfig(cfg *config.EdgeConfig) (*SimStore, error) {
	simStore := &SimStore{
		servers:       make(map[string]*mbserver.Server),
		deviceConfigs: make(map[string]*SimDeviceConfig),
		profiles:      make(map[string]DeviceProfile),
	}

	for _, bus := range cfg.Buses {

		devices := cfg.Devices[bus.BusId]
		if len(devices) == 0 {
			continue
		}

		s := mbserver.NewServer()
		simStore.servers[bus.BusId] = s

		for _, device := range devices {
			id := device.UnitId
			if id != 1 {
				if err := s.NewDevice(id); err != nil {
					return nil, fmt.Errorf("NewDevice(%d) on bus %s: %w", id, bus.BusId, err)
				}
			}

			simDevConfig := &SimDeviceConfig{
				Name:   device.Name,
				BusId:  bus.BusId,
				UnitID: uint8(device.UnitId),
			}
			if device.CatalogSpec.DigitalOutputs != nil {
				simDevConfig.DigitalOutputsStart = device.CatalogSpec.DigitalOutputs.Start
				simDevConfig.DigitalOutputs = device.CatalogSpec.DigitalOutputs.Count
			}
			if device.CatalogSpec.DigitalInputs != nil {
				simDevConfig.DigitalInputsStart = device.CatalogSpec.DigitalInputs.Start
				simDevConfig.DigitalInputs = device.CatalogSpec.DigitalInputs.Count
			}
			if device.CatalogSpec.AnalogOutputs != nil {
				simDevConfig.AnalogOutputsStart = device.CatalogSpec.AnalogOutputs.Start
				simDevConfig.AnalogOutputs = device.CatalogSpec.AnalogOutputs.Count
			}
			if device.CatalogSpec.AnalogInputs != nil {
				simDevConfig.AnalogInputsStart = device.CatalogSpec.AnalogInputs.Start
				simDevConfig.AnalogInputs = device.CatalogSpec.AnalogInputs.Count
			}
			simStore.deviceConfigs[device.Name] = simDevConfig
		}

		// Register profiles for known device types
		for _, device := range devices {
			if profile := createProfile(device.Type); profile != nil {
				if dev, ok := s.Devices[uint8(device.UnitId)]; ok {
					profile.Init(&dev)
				}
				simStore.profiles[device.Name] = profile
				log.Printf("  Profile registered: %s -> %s", device.Name, profile.Name())
			}
		}

		log.Printf("Simulator: bus %s configured with %d device(s)", bus.BusId, len(devices))
		for _, device := range devices {
			log.Printf("  - %s (UnitID: %d)", device.Name, device.UnitId)
		}
	}

	return simStore, nil
}

// GetServer returns the mbserver.Server for a bus, or nil.
func (s *SimStore) GetServer(busId string) *mbserver.Server {
	return s.servers[busId]
}

// GetDeviceConfig returns the SimDeviceConfig for a device, or nil.
func (s *SimStore) GetDeviceConfig(name string) *SimDeviceConfig {
	return s.deviceConfigs[name]
}

// GetDevice returns the mbserver.Device for a given bus + device name.
func (s *SimStore) GetDevice(busId, deviceName string) (*mbserver.Server, *mbserver.Device, *SimDeviceConfig, bool) {
	srv := s.GetServer(busId)
	if srv == nil {
		return nil, nil, nil, false
	}

	cfg := s.GetDeviceConfig(deviceName)
	if cfg == nil {
		return nil, nil, nil, false
	}

	dev, ok := srv.Devices[cfg.UnitID]
	if !ok {
		return nil, nil, nil, false
	}
	return srv, &dev, cfg, true
}

// createProfile returns a DeviceProfile for known device types, or nil.
func createProfile(deviceType string) DeviceProfile {
	switch deviceType {
	case "shelly.pro3em":
		return profiles.NewShellyPro3EM()
	default:
		return nil
	}
}
