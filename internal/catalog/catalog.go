package catalog

import (
	"context"
	"fmt"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/messaging"
)

type EdgeCatalogMessage struct {
	Devices []DeviceSummary `json:"devices"`
}

// CatalogResource describes a single resource on a device (IHC: individual resource IDs).
type CatalogResource struct {
	ID    int    `json:"id"`
	HexID string `json:"hexId"` // hex representation for debugging (e.g. "0x9F045C")
	Type  string `json:"type"`  // digitalOutput, digitalInput, analogOutput, analogInput
}

type DeviceSummary struct {
	Name              string             `json:"name"`
	UnitId            uint8              `json:"unitId,omitempty"`
	Type              string             `json:"type"`
	BusId             string             `json:"busId,omitempty"`
	BypassSignalState bool               `json:"bypassSignalState,omitempty"`
	Resources         []CatalogResource  `json:"resources,omitempty"`
	DigitalOutputs    *config.Range      `json:"digitalOutputs,omitempty"`
	DigitalInputs     *config.Range      `json:"digitalInputs,omitempty"`
	AnalogOutputs     *config.Range      `json:"analogOutputs,omitempty"`
	AnalogInputs      *config.Range      `json:"analogInputs,omitempty"`
}

type Catalog struct {
	cfg *config.EdgeConfig
}

func NewEdgeCatalog(cfg *config.EdgeConfig) *Catalog {
	cat := Catalog{
		cfg: cfg,
	}
	return &cat
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
			ID:    r.ResourceIntID,
			HexID: fmt.Sprintf("0x%X", r.ResourceIntID),
			Type:  r.Type,
		})
		}
		devices = append(devices, DeviceSummary{
			Name:              ctrl.Name,
			Type:              "ihc",
			BypassSignalState: true,
			Resources:         resources,
		})
	}

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
