package simulator

import (
	"log"
	"time"

	"github.com/goburrow/serial"

	"github.com/fisaks/uhn/internal/config"
)

// StartListenRTU opens serial ports for each RTU bus and starts listening.
// This blocks the calling goroutine for the last bus; call from a goroutine if needed.
func StartListenRTU(simStore *SimStore, buses []*config.BusConfig) error {
	for _, bus := range buses {
		if bus.Type != "rtu" {
			continue
		}

		srv := simStore.GetServer(bus.BusId)
		if srv == nil {
			log.Printf("RTU: no server for bus %s, skipping", bus.BusId)
			continue
		}

		go func(bus *config.BusConfig) {
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

			if err := srv.ListenRTU(port); err != nil {
				log.Fatalf("listenRTU on %s: %v", bus.Port, err)
			}
			log.Printf("RTU simulator ready on %s for bus %s", bus.Port, bus.BusId)

			select {} // block forever — server runs in background goroutines
		}(bus)
	}

	return nil
}
