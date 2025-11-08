package config

type EdgeCatalogMessage struct {
	Devices []DeviceSummary `json:"devices"`
}

type DeviceSummary struct {
	Name           string `json:"name"`
	UnitId         uint8  `json:"unitId"`
	Type           string `json:"type"`
	DigitalOutputs *Range `json:"digitalOutputs,omitempty"`
	DigitalInputs  *Range `json:"digitalInputs,omitempty"`
	AnalogOutputs  *Range `json:"analogOutputs,omitempty"`
	AnalogInputs   *Range `json:"analogInputs,omitempty"`
}

func BuildEdgeCatalog(cfg *EdgeConfig) (*EdgeCatalogMessage, error) {
	var devices []DeviceSummary
	for _, devs := range cfg.Devices {
		for _, d := range devs {
			devices = append(devices, DeviceSummary{
				Name:           d.Name,
				UnitId:         d.UnitId,
				Type:           d.Type,
				DigitalOutputs: cfg.Catalog[d.Type].DigitalOutputs,
				DigitalInputs:  cfg.Catalog[d.Type].DigitalInputs,
				AnalogOutputs:  cfg.Catalog[d.Type].AnalogOutputs,
				AnalogInputs:   cfg.Catalog[d.Type].AnalogInputs,
			})
		}
	}
	return &EdgeCatalogMessage{
		Devices: devices,
	}, nil
}
