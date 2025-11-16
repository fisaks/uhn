package poller

import (
	"context"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/uhn"
)

type BusPollers interface {
	uhn.EdgeSubscriber
	StartAllPollers(ctx context.Context)
	StopAllPollers()
	FindPollerAndDeviceByDeviceName(deviceName string) (BusPoller, *config.DeviceConfig)
}

type busPollers struct {
	pollers []BusPoller
}

func NewBusPollers(cfg *config.EdgeConfig, edgePublisher uhn.EdgePublisher) (BusPollers, error) {
	pollers := make([]BusPoller, len(cfg.Buses))

	for i, bus := range cfg.Buses {
		poller, err := NewSerialBusPoller(bus, edgePublisher)
		if err != nil {
			return nil, err
		}
		pollers[i] = poller
	}
	return &busPollers{pollers: pollers}, nil
}

func (p *busPollers) StartAllPollers(ctx context.Context) {
	for _, poller := range p.pollers {
		go poller.StartPoller(ctx)
	}
}

func (p *busPollers) StopAllPollers() {
	for _, poller := range p.pollers {
		poller.StopPoller()
	}
}

func (p *busPollers) FindPollerAndDeviceByDeviceName(deviceName string) (BusPoller, *config.DeviceConfig) {
	for _, poller := range p.pollers {
		for _, device := range poller.GetDevices() {
			if device.Name == deviceName {
				return poller, device
			}
		}
	}
	return nil, nil
}
