package mqtt

import (
	"encoding/json"

	MQTT "github.com/eclipse/paho.mqtt.golang"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/config"
)

func publishCatalog(client MQTT.Client, topic string, catalog *config.EdgeCatalogMessage) {
	data, err := json.Marshal(catalog)
	if err != nil {
		logging.Error("Failed to marshal catalog", "error", err)
		return
	}
	token := client.Publish(topic, 1, true, data) // QoS 1, retained
	token.Wait()
	if token.Error() != nil {
		logging.Error("Failed to publish catalog", "error", token.Error())
	} else {
		logging.Info("Published edge catalog", "topic", topic)
	}
}
