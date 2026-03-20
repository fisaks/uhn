package main

import (
	"log"
	"os"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/milightsim"
)

func main() {
	configPath := os.Getenv("SIM_CONFIG_PATH")
	if configPath == "" {
		log.Fatal("SIM_CONFIG_PATH not set")
	}

	restAddr := os.Getenv("MILIGHT_SIM_REST_ADDR")
	if restAddr == "" {
		restAddr = ":8091"
	}

	logging.Init()
	edgeConfig, err := config.LoadEdgeConfig(configPath)
	if err != nil {
		log.Fatalf("Edge config error: %v", err)
	}

	if len(edgeConfig.Milights) == 0 {
		log.Fatal("No Mi-Light gateways defined in config")
	}

	store, err := milightsim.SetupFromConfig(edgeConfig)
	if err != nil {
		log.Fatalf("Mi-Light sim setup error: %v", err)
	}

	udpServer := milightsim.NewUDPServer(store, store.Port())
	go func() {
		if err := udpServer.Start(); err != nil {
			log.Fatalf("Mi-Light UDP server error: %v", err)
		}
	}()

	if err := milightsim.StartRestAPI(store, restAddr, udpServer); err != nil {
		log.Fatalf("Mi-Light REST API error: %v", err)
	}

	select {}
}
