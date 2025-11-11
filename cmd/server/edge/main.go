package main

// cSpell:ignore mqtt modbusTCP mymqtt modbus
import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
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
	pollers, err := modbus.NewBusPollers(cfg, publisher)
	if err != nil {
		logging.Fatal("poller init: %v", err)
	}

	// Graceful shutdown context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start all bus pollers (one goroutine per bus)
	for _, p := range pollers {
		go p.Run(ctx)
	}

	cmdTopic := topicPrefix + "/device/+/cmd"
	if token := client.Subscribe(cmdTopic, 1, func(_ mqtt.Client, message mqtt.Message) {
		// Parse device name from topic
		parts := strings.Split(message.Topic(), "/")
		// uhn/<edge>/device/<deviceName>/cmd
		if len(parts) < 5 {
			logging.Warn("cmd topic malformed", "topic", message.Topic())
			return
		}
		deviceName := parts[3]

		var inCommand modbus.IncomingDeviceCommand
		if err := json.Unmarshal(message.Payload(), &inCommand); err != nil {
			logging.Warn("cmd json", "error", err)
			return
		}
		inCommand.Device = deviceName

		poller, dc, err := modbus.ResolveIncoming(pollers, inCommand, deviceName)
		if err != nil {
			modbus.PublishEvent(client, topicPrefix, deviceName, "commandError",
				map[string]any{"reason": "device not found", "device": deviceName})
			return
		}

		// Optional: validate address against catalog ranges (if you want)
		// (owner.Catalog[device.Type].DigitalOutputs, etc.)

		// Enqueue without blocking; drop if queue full
		select {
		case poller.CmdCh() <- *dc: // alternative: we have owner from ResolveIncoming? see note below
		default:
			modbus.PublishEvent(client, topicPrefix, deviceName, "commandDropped",
				map[string]any{"reason": "queue full", "action": dc.Action})
		}
	}); token.Wait() && token.Error() != nil {
		logging.Fatal("mqtt subscribe cmd", "error", token.Error())
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
