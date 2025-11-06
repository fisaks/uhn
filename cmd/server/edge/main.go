package main

// cSpell:ignore mqtt modbusTCP mymqtt modbus
import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/modbus"
	mymqtt "github.com/fisaks/uhn/internal/mqtt"
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {

	mqttURL := getenv("MQTT_URL", "tcp://localhost:1883")
	path := getenv("EDGE_CONFIG_PATH", "/etc/uhn/edge-config.json")
	edgeName := getenv("EDGE_NAME", "edge1")
	topicPrefix := "uhn/" + edgeName

	logging.Init()
	cfg, err := config.LoadEdgeConfig(path)
	if err != nil {
		logging.Fatal("Edge config error", "error", err)
	}

	logging.Info("Loaded config",
		"buses", len(cfg.Buses),
		"pollMs", cfg.PollIntervalMs,
	)

	catalog, err := config.BuildEdgeCatalog(cfg)
	if err != nil {
		logging.Fatal("Failed to build catalog message", "error", err)
	}

	client := mymqtt.MustConnect(mqttURL, edgeName, catalog)
	defer client.Disconnect(250)

	publisher := &modbus.Publisher{Client: client, TopicPrefix: topicPrefix}

	// Build the poller set grouped by bus
	bp, err := modbus.NewBusPollers(cfg, publisher)
	if err != nil {
		logging.Fatal("poller init: %v", err)
	}

	// Graceful shutdown context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start all bus pollers (one goroutine per bus)
	for _, p := range bp {
		go p.Run(ctx)
	}

	// Wait for SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	s := <-sigCh
	logging.Info("Shutting down", "signal", s)

	// Give pollers a moment to exit cleanly (they honor ctx)
	cancel()
	time.Sleep(200 * time.Millisecond)
	logging.Info("bye")
}
