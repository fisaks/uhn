package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
	"github.com/fisaks/uhn/internal/zigbeesim"
)

func main() {
	logging.Init()

	devicesPath := getEnv("ZIGBEE_SIM_DEVICES", "config/zigbee-sim-devices.json")
	statePath := getEnv("ZIGBEE_SIM_STATE", "config/zigbee-sim-state.json")
	mqttURL := getEnv("ZIGBEE_SIM_MQTT", "tcp://localhost:1883")
	baseTopic := getEnv("ZIGBEE_SIM_BASE_TOPIC", "zigbee2mqtt-sim")

	restPort := 8092

	store, err := zigbeesim.NewZ2MSimStore(devicesPath, statePath)
	if err != nil {
		logging.Fatal("Failed to load Z2M sim fixtures", "error", err)
	}

	logging.Info("Z2M simulator starting",
		"devices", len(store.DeviceNames()),
		"mqtt", mqttURL,
		"baseTopic", baseTopic,
		"restPort", restPort)

	broker := messaging.NewBroker(messaging.BrokerConfig{
		BrokerURL:        mqttURL,
		ClientName:       "zigbee-sim",
		TopicPrefix:      "",
		CleanSession:     true,
		ConnectTimeout:   10 * time.Second,
		PublishTimeout:   5 * time.Second,
		SubscribeTimeout: 5 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Delay startup to ensure the edge has subscribed before we publish
	// retained messages (retained only delivered at subscribe time).

	if err := broker.Connect(ctx); err != nil {
		logging.Fatal("MQTT connect failed", "error", err)
	}
	defer broker.Close(ctx)

	server := zigbeesim.NewZ2MSimServer(store, broker, baseTopic, 10000) // tick every 10s

	// REST control plane
	rest := zigbeesim.NewZ2MSimREST(store, server)
	go func() {
		if err := rest.Start(restPort); err != nil {
			logging.Fatal("REST server failed", "error", err)
		}
	}()

	// Start MQTT server (blocks until ctx cancelled)
	go func() {
		if err := server.Start(ctx); err != nil && ctx.Err() == nil {
			logging.Fatal("Z2M sim server failed", "error", err)
		}
	}()

	// Wait for shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	s := <-sigCh
	logging.Info("Shutting down", "signal", s)
	cancel()
	time.Sleep(200 * time.Millisecond)
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
