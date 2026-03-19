package main

import (
	"log"
	"os"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/ihcsim"
	"github.com/fisaks/uhn/internal/logging"
)

func main() {
	configPath := os.Getenv("SIM_CONFIG_PATH")
	if configPath == "" {
		log.Fatal("SIM_CONFIG_PATH not set")
	}

	restAddr := os.Getenv("IHC_SIM_REST_ADDR")
	if restAddr == "" {
		restAddr = ":8090"
	}

	logging.Init()
	edgeConfig, err := config.LoadEdgeConfig(configPath)
	if err != nil {
		log.Fatalf("Edge config error: %v", err)
	}

	if len(edgeConfig.IHCControllers) == 0 {
		log.Fatal("No IHC controllers defined in config")
	}

	store, err := ihcsim.SetupFromConfig(edgeConfig)
	if err != nil {
		log.Fatalf("IHC sim setup error: %v", err)
	}

	// Load bindings config if available
	bindingsPath := os.Getenv("IHC_SIM_BINDINGS_PATH")
	if bindingsPath == "" {
		bindingsPath = "config/ihc-sim-bindings.json"
	}
	if _, statErr := os.Stat(bindingsPath); statErr == nil {
		if err := store.BindingManager().LoadFromFile(bindingsPath); err != nil {
			log.Printf("Warning: failed to load bindings from %s: %v", bindingsPath, err)
		} else {
			log.Printf("Loaded bindings from %s", bindingsPath)
		}
	}

	// Start SOAP servers for each controller
	soapServers := make(map[string]*ihcsim.SOAPServer)
	for _, name := range store.Controllers() {
		ctrl := store.GetController(name)
		server := ihcsim.NewSOAPServer(ctrl)
		if err := server.Start(); err != nil {
			log.Fatalf("Failed to start SOAP server for %s: %v", name, err)
		}
		soapServers[name] = server
	}

	// Start REST control plane
	if err := ihcsim.StartRestAPI(store, restAddr, soapServers); err != nil {
		log.Fatalf("REST API error: %v", err)
	}

	select {}
}
