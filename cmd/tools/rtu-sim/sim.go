package main

import (
	"log"
	"sync"
	"time"

	"github.com/goburrow/serial"
	"github.com/womat/mbserver"

	"github.com/fisaks/uhn/internal/config"
	// other imports as needed
)

type SimDeviceConfig struct {
	Name           string
	UnitID         uint8
	DigitalOutputs uint16
	DigitalInputs  uint16
	AnalogOutputs  uint16
	AnalogInputs   uint16
	// other fields as needed
}

var (
	simulators      = make(map[string]*mbserver.Server) // busId => server
	simulatorsMu    sync.RWMutex
	deviceConfigs   = make(map[string]*SimDeviceConfig) // deviceID => SimDeviceConfig
	deviceConfigsMu sync.RWMutex
)

// StartRTUSim launches a simulator for each bus in config.
func StartRTUSim(edgeConfig *config.EdgeConfig) error {
	for _, bus := range edgeConfig.Buses {
		if bus.Type != "rtu" {
			continue
		}
		go runBusSimulator(bus, edgeConfig.Devices[bus.BusId])
	}
	return nil
}

func runBusSimulator(bus *config.BusConfig, devices []*config.DeviceConfig) {
	s := mbserver.NewServer()
	simulatorsMu.Lock()
	simulators[bus.BusId] = s
	simulatorsMu.Unlock()

	deviceConfigsMu.Lock()
	for _, device := range devices {
		id := device.UnitId
		if id != 1 {
			if err := s.NewDevice(id); err != nil {
				log.Fatalf("NewDevice(%d): %v", id, err)
			}
		}

		simDevConfig := &SimDeviceConfig{
			Name:   device.Name,
			UnitID: uint8(device.UnitId),
		}
		if device.CatalogSpec.DigitalOutputs != nil {
			simDevConfig.DigitalOutputs = device.CatalogSpec.DigitalOutputs.Count
		}
		if device.CatalogSpec.DigitalInputs != nil {
			simDevConfig.DigitalInputs = device.CatalogSpec.DigitalInputs.Count
		}
		if device.CatalogSpec.AnalogOutputs != nil {
			simDevConfig.AnalogOutputs = device.CatalogSpec.AnalogOutputs.Count
		}
		if device.CatalogSpec.AnalogInputs != nil {
			simDevConfig.AnalogInputs = device.CatalogSpec.AnalogInputs.Count
		}
		deviceConfigs[device.Name] = simDevConfig

	}
	deviceConfigsMu.Unlock()
	port, err := serial.Open(&serial.Config{
		Address:  bus.Port,
		BaudRate: bus.Baud,
		DataBits: bus.DataBits,
		StopBits: bus.StopBits,
		Parity:   bus.Parity,
		Timeout:  2 * time.Second,
	})
	if err != nil {
		log.Fatalf("serial open %s: %v", bus.Port, err)
	}
	defer port.Close()

	if err := s.ListenRTU(port); err != nil {
		log.Fatalf("listenRTU: %v", err)
	}
	log.Printf("RTU simulator ready on %s for bus %s (devices: %d)", bus.Port, bus.BusId, len(devices))
	for _, device := range devices {
		log.Printf("  - %s (UnitID: %d)", device.Name, device.UnitId)
	}
	for {
		time.Sleep(1 * time.Second)
	}

}
