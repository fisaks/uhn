package zigbeesim

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
)

// Z2MSimServer simulates a Zigbee2MQTT instance on MQTT.
type Z2MSimServer struct {
	store     *Z2MSimStore
	broker    messaging.Broker
	baseTopic string
	tickerMs  int
}

// NewZ2MSimServer creates a Z2M simulator MQTT server.
func NewZ2MSimServer(store *Z2MSimStore, broker messaging.Broker, baseTopic string, tickerMs int) *Z2MSimServer {
	return &Z2MSimServer{
		store:     store,
		broker:    broker,
		baseTopic: baseTopic,
		tickerMs:  tickerMs,
	}
}

// Start publishes initial state, subscribes to /set commands, and runs the ticker.
func (s *Z2MSimServer) Start(ctx context.Context) error {
	// Publish bridge/state
	s.broker.PublishRaw(ctx, s.baseTopic+"/bridge/state", messaging.FireAndForget, true,
		[]byte(`{"state":"online"}`))

	// Publish bridge/devices (retained)
	s.broker.PublishRaw(ctx, s.baseTopic+"/bridge/devices", messaging.FireAndForget, true,
		s.store.BridgeDevicesJSON())
	logging.Info("Z2M sim: published bridge/devices", "devices", len(s.store.DeviceNames()))

	// Publish initial device state + availability
	for _, name := range s.store.DeviceNames() {
		data, err := s.store.GetState(name)
		if err != nil {
			continue
		}
		s.broker.PublishRaw(ctx, s.baseTopic+"/"+name, messaging.FireAndForget, true, data)
		s.broker.PublishRaw(ctx, s.baseTopic+"/"+name+"/availability", messaging.FireAndForget, true,
			[]byte(`{"state":"online"}`))
	}
	logging.Info("Z2M sim: published initial device state")

	// Subscribe to /set commands
	s.broker.SubscribeRaw(ctx, s.baseTopic+"/+/set", messaging.FireAndForget, &setHandler{sim: s})

	// Subscribe to bridge/request/device/options (respond with ok)
	s.broker.SubscribeRaw(ctx, s.baseTopic+"/bridge/request/device/options", messaging.FireAndForget,
		&optionsHandler{sim: s})


	// Run ticker for sensor simulation
	if s.tickerMs > 0 {
		go s.runTicker(ctx)
	}

	logging.Info("Z2M sim: started", "baseTopic", s.baseTopic)
	<-ctx.Done()
	return nil
}

// PublishBridgeDevices publishes bridge/devices to MQTT (non-retained).
func (s *Z2MSimServer) PublishBridgeDevices(ctx context.Context) {
	data := s.store.BridgeDevicesJSON()
	// Use QoS 0 (fire and forget) — QoS 1 with persistent sessions can
	// cause delivery issues between two Paho Go clients on the same broker.
	err := s.broker.PublishRaw(ctx, s.baseTopic+"/bridge/devices", messaging.FireAndForget, false, data)
	logging.Info("Z2M sim: published bridge/devices (manual)",
		"topic", s.baseTopic+"/bridge/devices",
		"payloadLen", len(data),
		"error", err)
}

// PublishDeviceState publishes a device state blob to MQTT (non-retained for action events).
func (s *Z2MSimServer) PublishDeviceState(deviceName string, data []byte) {
	topic := s.baseTopic + "/" + deviceName
	s.broker.PublishRaw(context.Background(), topic, messaging.FireAndForget, false, data)
	logging.Debug("Z2M sim: published device state", "topic", topic, "payloadLen", len(data))
}

func (s *Z2MSimServer) runTicker(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.tickerMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			changed := s.store.SimulateTick()
			for name, data := range changed {
				s.broker.PublishRaw(ctx, s.baseTopic+"/"+name, messaging.FireAndForget, true, data)
			}
		}
	}
}

// setHandler handles {baseTopic}/{device}/set messages.
type setHandler struct {
	sim *Z2MSimServer
}

func (h *setHandler) OnMessage(ctx context.Context, topic string, payload []byte, _ bool) {
	// Extract device name: {baseTopic}/{device}/set
	prefix := h.sim.baseTopic + "/"
	if !strings.HasPrefix(topic, prefix) {
		return
	}
	remainder := topic[len(prefix):]
	device := strings.TrimSuffix(remainder, "/set")
	if device == remainder {
		return // no /set suffix
	}

	// Parse the set payload
	var setCmd map[string]any
	if err := json.Unmarshal(payload, &setCmd); err != nil {
		logging.Warn("Z2M sim: invalid /set payload", "device", device, "error", err)
		return
	}

	logging.Debug("Z2M sim: /set command", "device", device, "payload", setCmd)

	// Apply each property
	for prop, value := range setCmd {
		if prop == "state" {
			// Handle ON/OFF/TOGGLE
			strVal, _ := value.(string)
			switch strings.ToUpper(strVal) {
			case "TOGGLE":
				data, err := h.sim.store.ToggleState(device)
				if err != nil {
					logging.Warn("Z2M sim: toggle failed", "device", device, "error", err)
					return
				}
				h.sim.broker.PublishRaw(ctx, h.sim.baseTopic+"/"+device, messaging.FireAndForget, true, data)
				return
			case "ON":
				value = "ON"
			case "OFF":
				value = "OFF"
			}
		}
		data, err := h.sim.store.SetProperty(device, prop, value)
		if err != nil {
			logging.Warn("Z2M sim: set property failed", "device", device, "property", prop, "error", err)
			return
		}
		h.sim.broker.PublishRaw(ctx, h.sim.baseTopic+"/"+device, messaging.FireAndForget, true, data)
	}
}

// optionsHandler handles bridge/request/device/options messages.
type optionsHandler struct {
	sim *Z2MSimServer
}

func (h *optionsHandler) OnMessage(ctx context.Context, topic string, payload []byte, _ bool) {
	// Just respond with ok
	var req struct {
		ID      string         `json:"id"`
		Options map[string]any `json:"options"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		return
	}

	resp := map[string]any{
		"status": "ok",
		"data": map[string]any{
			"id":               req.ID,
			"from":             req.Options,
			"to":               req.Options,
			"restart_required": false,
		},
	}
	data, _ := json.Marshal(resp)
	h.sim.broker.PublishRaw(ctx, h.sim.baseTopic+"/bridge/response/device/options",
		messaging.FireAndForget, false, data)

	logging.Debug("Z2M sim: device options request", "device", req.ID, "options", req.Options)
}
