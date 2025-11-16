package catalog

import (
	"context"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/messaging"
)

type EdgeCatalogMessage struct {
	Devices []DeviceSummary `json:"devices"`
}

type DeviceSummary struct {
	Name           string        `json:"name"`
	UnitId         uint8         `json:"unitId"`
	Type           string        `json:"type"`
	BusId          string        `json:"busId"`
	DigitalOutputs *config.Range `json:"digitalOutputs,omitempty"`
	DigitalInputs  *config.Range `json:"digitalInputs,omitempty"`
	AnalogOutputs  *config.Range `json:"analogOutputs,omitempty"`
	AnalogInputs   *config.Range `json:"analogInputs,omitempty"`
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
func (catalog *Catalog) buildEdgeCatalog() (*EdgeCatalogMessage, error) {
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
	return &EdgeCatalogMessage{
		Devices: devices,
	}, nil
}
func (catalog *Catalog) OnConnectPublish(ctx context.Context) (*messaging.ConnectMessage, error) {
	msg, err := catalog.buildEdgeCatalog()
	if err != nil {
		logging.Fatal("Failed to build catalog message", "error", err)
	}
	return &messaging.ConnectMessage{

		Topic:   "catalog",
		Qos:     messaging.AtLeastOnce,
		Retain:  true,
		Payload: msg,
	}, nil
}
