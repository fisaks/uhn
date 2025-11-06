package config

type EdgeCatalogMessage struct {
	Devices []DeviceSummary `json:"devices"`
}

type DeviceSummary struct {
	Name           string `json:"name"`
	UnitId         uint8  `json:"unitId"`
	Type           string `json:"type"`
	Coils          *Range `json:"coils,omitempty"`
	DiscreteInputs *Range `json:"discreteInputs,omitempty"`
	HoldingRegs    *Range `json:"holdingRegisters,omitempty"`
	InputRegs      *Range `json:"inputRegisters,omitempty"`
}

func BuildEdgeCatalog(cfg *EdgeConfig) (*EdgeCatalogMessage, error) {
	var devices []DeviceSummary
	for _, devs := range cfg.Devices {
		for _, d := range devs {
			devices = append(devices, DeviceSummary{
				Name:           d.Name,
				UnitId:         d.UnitId,
				Type:           d.Type,
				Coils:          cfg.Catalog[d.Type].Coils,
				DiscreteInputs: cfg.Catalog[d.Type].DiscreteInputs,
				HoldingRegs:    cfg.Catalog[d.Type].HoldingRegs,
				InputRegs:      cfg.Catalog[d.Type].InputRegs,
			})
		}
	}
	return &EdgeCatalogMessage{
		Devices: devices,
	}, nil
}
