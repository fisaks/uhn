package mqtt

// cSpell:ignore mqtt
import (
	"github.com/fisaks/uhn/internal/logging"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func MustConnect(brokerURL, clientID string) mqtt.Client {
	opts := mqtt.NewClientOptions().AddBroker(brokerURL)
	opts.SetClientID(clientID)
	opts.SetAutoReconnect(true)
	c := mqtt.NewClient(opts)
	if tok := c.Connect(); tok.Wait() && tok.Error() != nil {
		logging.Error("mqtt connect: %v", tok.Error())
	}
	return c
}
