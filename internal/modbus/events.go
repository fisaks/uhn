package modbus

import (
	"encoding/json"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fisaks/uhn/internal/logging"
)

func PublishEvent(c mqtt.Client, prefix, device, typ string, detail map[string]any) {
	msg := map[string]any{
		"type":   typ,
		"ts":     time.Now().UTC().Format(time.RFC3339),
		"detail": detail,
	}
	b, _ := json.Marshal(msg)
	t := prefix + "/device/" + device + "/event"
	token := c.Publish(t, 1, false, b)
	token.Wait()
}

// Publish device state to MQTT
func (p *BusPoller) publishDeviceState(deviceName string, state DeviceState, retain bool) {
	topic := p.Publisher.TopicPrefix + "/device/" + deviceName + "/state"
	payload, err := json.Marshal(state)
	if err != nil {
		logging.Error("BusPoller marshal state", "bus", p.Bus.BusId, "device", deviceName, "error", err)
		return
	}
	token := p.Publisher.Client.Publish(topic, 0, retain, payload)
	token.Wait()
	if token.Error() != nil {
		logging.Error("BusPoller mqtt publish", "bus", p.Bus.BusId, "device", deviceName, "error", token.Error())
	}
}
