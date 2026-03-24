package runtime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/uhn"
)

// ResourceSignalSubscriber implements messaging.Subscriber to handle incoming
// signal override MQTT messages on the topic "resource/signal/+".
// It catches master-originated signals and applies them to the IPC bridge,
// while filtering out self-published signals via SignalTracker.
//
// For resources that belong to a driver that owns physical state (e.g. IHC),
// signals are forwarded to the driver's controller instead of setting local
// signal state (S). The controller is the source of truth — state flows back
// through the driver's notification mechanism as physical state (P).
type ResourceSignalSubscriber struct {
	ipcBridge     *IPCBridge
	signalTracker *SignalTracker
	drivers       map[string]uhn.DeviceDriver
}

// NewResourceSignalSubscriber creates a new resource signal subscriber.
func NewResourceSignalSubscriber(ipcBridge *IPCBridge, signalTracker *SignalTracker) *ResourceSignalSubscriber {
	return &ResourceSignalSubscriber{
		ipcBridge:     ipcBridge,
		signalTracker: signalTracker,
	}
}

// SetDrivers sets the device drivers for signal forwarding.
func (s *ResourceSignalSubscriber) SetDrivers(drivers map[string]uhn.DeviceDriver) {
	s.drivers = drivers
}

// OnMessage handles an incoming MQTT message on "resource/signal/{resourceId}".
// Topic format after prefix stripping: "uhn/{edge}/resource/signal/{resourceId}"
func (s *ResourceSignalSubscriber) OnMessage(ctx context.Context, topic string, payload []byte, _ bool) {
	// Topic format: "uhn/{edge}/resource/signal/{resourceId}"
	parts := strings.Split(topic, "/")
	if len(parts) != 5 {
		logging.Warn("ResourceSignalSubscriber: malformed topic", "topic", topic)
		return
	}
	resourceID := parts[4]

	var msg SignalMQTTPayload
	if err := json.Unmarshal(payload, &msg); err != nil {
		logging.Warn("ResourceSignalSubscriber: invalid JSON", "topic", topic, "error", err)
		return
	}

	// Skip self-published signals
	if s.signalTracker.IsEcho(resourceID, msg.Timestamp) {
		logging.Debug("ResourceSignalSubscriber: skipping self-echo", "resourceId", resourceID)
		return
	}

	// Check if this resource belongs to a driver that owns physical state (e.g. IHC).
	// If so, forward to the controller instead of setting local signal state.
	if s.drivers != nil {
		rm := s.ipcBridge.getResourceMap()
		if rm != nil {
			if resource, ok := rm.LookupResource(resourceID); ok && resource.Pin != nil {
				if driver, dOk := s.drivers[resource.Device]; dOk && driver.BypassSignalState() {
					logging.Debug("ResourceSignalSubscriber: forwarding signal to driver",
						"resourceId", resourceID, "device", resource.Device, "pin", config.FormatPin(resource.Pin))
					if err := driver.HandleSignal(ctx, resource.Pin, msg.Value); err != nil {
						logging.Error("ResourceSignalSubscriber: driver signal error",
							"resourceId", resourceID, "device", resource.Device, "error", err)
					}
					return
				}
			}
		}
	}

	// Default: apply as local signal state override
	logging.Debug("ResourceSignalSubscriber: applying signal", "resourceId", resourceID, "value", msg.Value)
	s.ipcBridge.HandleSignalUpdate(resourceID, msg.Value, msg.Timestamp)
}
