package catalog

import (
	"context"
	"sync"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
)

type EdgeCatalogMessage struct {
	Devices []DeviceSummary `json:"devices"`
}

// CatalogResource describes a single resource on a device.
// ID is numeric for IHC (resource ID) and string for Z2M (property name).
type CatalogResource struct {
	ID   any    `json:"id"`   // int (IHC) or string (Z2M property name)
	Type string `json:"type"` // digitalOutput, digitalInput, analogOutput, analogInput
}

type DeviceSummary struct {
	Name              string             `json:"name"`
	UnitId            uint8              `json:"unitId,omitempty"`
	Type              string             `json:"type"`
	BusId             string             `json:"busId,omitempty"`
	BypassSignalState bool               `json:"bypassSignalState,omitempty"`
	AssumedState      bool               `json:"assumedState,omitempty"`
	Resources         []CatalogResource  `json:"resources,omitempty"`
	DigitalOutputs    *config.Range      `json:"digitalOutputs,omitempty"`
	DigitalInputs     *config.Range      `json:"digitalInputs,omitempty"`
	AnalogOutputs     *config.Range      `json:"analogOutputs,omitempty"`
	AnalogInputs      *config.Range      `json:"analogInputs,omitempty"`
}

type Catalog struct {
	cfg    *config.EdgeConfig
	broker messaging.Broker

	// Z2M devices added dynamically after bridge/devices discovery
	zigbeeDevicesMu sync.RWMutex
	zigbeeDevices   []DeviceSummary
}

func NewEdgeCatalog(cfg *config.EdgeConfig) *Catalog {
	cat := Catalog{
		cfg: cfg,
	}
	return &cat
}

// SetBroker sets the MQTT broker for republishing catalog on Z2M discovery.
func (catalog *Catalog) SetBroker(broker messaging.Broker) {
	catalog.broker = broker
}

// AddZigbeeDevices adds Z2M devices to the catalog and republishes.
func (catalog *Catalog) AddZigbeeDevices(ctx context.Context, devices []DeviceSummary) {
	catalog.zigbeeDevicesMu.Lock()
	catalog.zigbeeDevices = devices
	catalog.zigbeeDevicesMu.Unlock()

	// Republish catalog with Z2M devices
	if catalog.broker != nil {
		msg := catalog.buildEdgeCatalog()
		if err := catalog.broker.PublishJSON(ctx, "catalog", messaging.AtLeastOnce, true, msg); err != nil {
			logging.Error("Catalog: failed to republish after Z2M discovery", "error", err)
		} else {
			logging.Info("Catalog: republished with Z2M devices", "zigbeeDevices", len(devices))
		}
	}
}

func (catalog *Catalog) buildEdgeCatalog() *EdgeCatalogMessage {
	var devices []DeviceSummary
	for _, devs := range catalog.cfg.Devices {
		for _, d := range devs {
			devices = append(devices, DeviceSummary{
				Name:           d.Name,
				UnitId:         d.UnitId,
				Type:           d.Type,
				BusId:          d.Bus.BusId,
				DigitalOutputs: d.CatalogSpec.DigitalOutputs,
				DigitalInputs:  d.CatalogSpec.DigitalInputs,
				AnalogOutputs:  d.CatalogSpec.AnalogOutputs,
				AnalogInputs:   d.CatalogSpec.AnalogInputs,
			})
		}
	}

	// Include IHC controllers as devices. IHC uses scattered resource IDs
	// (not contiguous ranges), so we list individual resources instead of ranges.
	for _, ctrl := range catalog.cfg.IHCControllers {
		var resources []CatalogResource
		for _, r := range ctrl.Resources {
			resources = append(resources, CatalogResource{
			ID:   r.ResourceIntID,
			Type: r.Type,
		})
		}
		devices = append(devices, DeviceSummary{
			Name:              ctrl.Name,
			Type:              "ihc",
			BypassSignalState: true,
			Resources:         resources,
		})
	}

	// Include Mi-Light zones as devices with assumed state.
	for _, ml := range catalog.cfg.Milights {
		for _, zone := range ml.Zones {
			devices = append(devices, DeviceSummary{
				Name:              zone.Name,
				Type:              "milight-" + zone.Model,
				BypassSignalState: true,
				AssumedState:      true,
				DigitalOutputs:    &config.Range{Start: 0, Count: 2},
				DigitalInputs:     &config.Range{Start: 2, Count: 3},
				AnalogOutputs:     &config.Range{Start: 5, Count: 5},
			})
		}
	}

	// Include Z2M devices (dynamically discovered)
	catalog.zigbeeDevicesMu.RLock()
	devices = append(devices, catalog.zigbeeDevices...)
	catalog.zigbeeDevicesMu.RUnlock()

	return &EdgeCatalogMessage{Devices: devices}
}

func (catalog *Catalog) OnConnectPublish(ctx context.Context) (*messaging.ConnectMessage, error) {
	return &messaging.ConnectMessage{
		Topic:   "catalog",
		Qos:     messaging.AtLeastOnce,
		Retain:  true,
		Payload: catalog.buildEdgeCatalog(),
	}, nil
}
