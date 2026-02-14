package main

// cSpell:ignore mqtt modbusTCP mymqtt modbus
import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fisaks/uhn/internal/blueprint"
	"github.com/fisaks/uhn/internal/catalog"
	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/encrypt"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
	"github.com/fisaks/uhn/internal/poller"
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
	catalog := catalog.NewEdgeCatalog(cfg)
	keyPair,err := encrypt.NewEdgeKeyPair(edgeName, path)
	if err != nil {
		logging.Fatal("Failed to load or create edge key pair", "error", err)
	}
	// Graceful shutdown context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	edgeBroker := messaging.NewEdgeBroker(messaging.BrokerConfig{
		BrokerURL:        mqttURL,
		ClientName:       edgeName,
		TopicPrefix:      topicPrefix,
		ConnectTimeout:   10 * time.Second,
		PublishTimeout:   5 * time.Second,
		SubscribeTimeout: 5 * time.Second,
	}, catalog, time.Duration(cfg.HeartbeatInterval)*time.Second)

	edgeBroker.AddOnConnectPublisher("identity", keyPair)
	edgeBroker.Connect(ctx)
	defer edgeBroker.Close(ctx)

	workspacePath := getenv("UHN_WORKSPACE_PATH", "")
	if workspacePath != "" {
		bpDownloader := blueprint.NewBlueprintDownloader(edgeName, keyPair, workspacePath)
		edgeBroker.SubscribeMaster(ctx, "identity", messaging.AtLeastOnce, bpDownloader.IdentitySubscriber())
		edgeBroker.SubscribeMaster(ctx, "blueprint/activated", messaging.AtLeastOnce, bpDownloader.BlueprintSubscriber())
	} else {
		logging.Info("UHN_WORKSPACE_PATH not set, blueprint downloader not activated")
	}

	pollers, err := poller.NewBusPollers(cfg, edgeBroker)
	if err != nil {
		logging.Fatal("poller init: %v", err)
	}
	edgeBroker.StartEdgeSubscriber(ctx, pollers)

	// Start all bus pollers (one goroutine per bus)
	pollers.StartAllPollers(ctx)
	defer pollers.StopAllPollers()

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
