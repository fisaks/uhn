package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/simulator"
)

func main() {
	configPath := os.Getenv("SIM_CONFIG_PATH")
	if configPath == "" {
		log.Fatal("SIM_CONFIG_PATH not set")
	}

	restAddr := os.Getenv("SIM_REST_ADDR")
	if restAddr == "" {
		restAddr = ":8080"
	}

	logging.Init()
	edgeConfig, err := config.LoadEdgeConfig(configPath)
	if err != nil {
		log.Fatalf("Edge config error: %v", err)
	}

	// Set up all buses (no filter)
	simStore, err := simulator.SetupFromConfig(edgeConfig)
	if err != nil {
		log.Fatalf("Simulator setup error: %v", err)
	}

	// Start RTU listeners (if any RTU buses exist)
	if err := simulator.StartListenRTU(simStore, edgeConfig.Buses); err != nil {
		log.Fatalf("RTU listen error: %v", err)
	}

	// Start TCP listeners (if any TCP buses exist)
	tcpCtrl, err := simulator.StartListenTCP(simStore, edgeConfig.Buses)
	if err != nil {
		log.Fatalf("TCP listen error: %v", err)
	}

	// Start profile ticker for virtual devices (e.g., Shelly Pro 3EM)
	simStore.StartProfileTicker(context.Background(), 10*time.Second)

	// Single REST API serving all buses
	if err := simulator.StartRestAPI(simStore, restAddr, tcpCtrl); err != nil {
		log.Fatalf("REST API error: %v", err)
	}

	select {}
}
