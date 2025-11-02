package main

// cSpell:ignore mqtt modbusTCP mymqtt modbus
import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"time"

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
	modbusTCP := getenv("MODBUS_TCP_ADDR", "localhost:1502")
	pollMs, _ := strconv.Atoi(getenv("POLL_PERIOD_MS", "1000"))
	unitI64, _ := strconv.ParseInt(getenv("UNIT_ID", "1"), 10, 64)

	client := mymqtt.MustConnect(mqttURL, "ihc-edge-"+strconv.FormatInt(time.Now().Unix(), 10))
	defer client.Disconnect(250)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	p := modbus.Poller{
		MQTTCli:     client,
		TCPAddr:     modbusTCP,
		Period:      time.Duration(pollMs) * time.Millisecond,
		UnitID:      byte(unitI64),
		TopicPrefix: "ihc/edge1",
	}

	log.Printf("Starting poller: modbus=%s mqtt=%s", modbusTCP, mqttURL)
	if err := p.Start(ctx); err != nil {
		log.Fatalf("poller exited: %v", err)
	}
}
