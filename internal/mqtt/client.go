package mqtt

// cSpell:ignore mqtt
import (
	"github.com/fisaks/uhn/internal/config"
	
	"github.com/fisaks/uhn/internal/logging"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func MustConnect(brokerURL, clientName string, catalog *config.EdgeCatalogMessage) mqtt.Client {
	opts := mqtt.NewClientOptions().AddBroker(brokerURL)
	opts.SetClientID("uhn-" + clientName)
	opts.SetAutoReconnect(true)
	opts.OnConnect = func(c mqtt.Client) {
		publishCatalog(c, "uhn/catalog/"+clientName	, catalog)
	}
	c := mqtt.NewClient(opts)
	if tok := c.Connect(); tok.Wait() && tok.Error() != nil {
		logging.Fatal("mqtt connect error","Error", tok.Error())
	}
	return c
}
