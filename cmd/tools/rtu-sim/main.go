package main

import (
	"log"
	"os"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
)

func main() {
	configPath := os.Getenv("SIM_CONFIG_PATH")
	if configPath == "" {
		log.Fatal("SIM_CONFIG_PATH not set")
	}
	logging.Init()
	edgeConfig, err := config.LoadEdgeConfig(configPath)
	if err != nil {
		log.Fatalf("Edge config error: %v", err)
	}

	// Start simulators
	if err := StartRTUSim(edgeConfig); err != nil {
		log.Fatalf("RTU sim error: %v", err)
	}

	// Start REST API
	go StartRestAPI() // Start in background, see rest.go

	// Wait forever
	select {}
}
