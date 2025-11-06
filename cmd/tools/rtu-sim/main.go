package main

import (
	"log"
	"os"
	"time"

	"github.com/fisaks/uhn/internal/config"
	"github.com/goburrow/serial"
	"github.com/womat/mbserver"
)

func main() {
	configPath := os.Getenv("SIM_CONFIG_PATH")
	if configPath == "" {
		log.Fatal("SIM_CONFIG_PATH not set")
	}
	edgeConfig, err := config.LoadEdgeConfig(configPath)
	if err != nil {
		log.Fatalf("Edge config error", "error", err)
	}

	for _, bus := range edgeConfig.Buses {
		if bus.Type != "rtu" {
			continue
		}
		go func(bus config.BusConfig) {
			runBusSimulator(bus, edgeConfig.Devices[bus.BusId])
		}(bus)
	}

	// Wait forever
	select {} // Wait forever

}

func runBusSimulator(bus config.BusConfig, devices []config.DeviceConfig) {
	s := mbserver.NewServer()

	for _, devConf := range devices {
		id := uint8(devConf.UnitId)
		if id != 1 {
			if err := s.NewDevice(id); err != nil {
				log.Fatalf("NewDevice(%d): %v", id, err)
			}
		}
		// TODO: Customize seeding as needed
		s.Devices[id].Coils[0] = 1
		//s.Devices[id].Coils[1] = 0
		//s.Devices[id].Coils[2] = 1

		s.Devices[id].DiscreteInputs[0] = 0
		//s.Devices[id].DiscreteInputs[1] = 1
	}

	
	port, err := serial.Open(&serial.Config{
		Address:  bus.Port,
		BaudRate: bus.Baud,
		DataBits: bus.DataBits,
		StopBits: bus.StopBits,
		Parity:   bus.Parity,
		Timeout:  2 * time.Second,
	})
	if err != nil {
		log.Fatalf("serial open %s: %v",  bus.Port, err)
	}
	defer port.Close()

	if err := s.ListenRTU(port); err != nil {
		log.Fatalf("listenRTU: %v", err)
	}
	log.Printf("RTU simulator ready on %s for bus %s (devices: %v)",  bus.Port, bus.BusId, devices)
	for {
		time.Sleep(1 * time.Second)
	}
}
