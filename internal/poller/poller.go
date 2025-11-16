package poller

import (
	"context"
	"fmt"
	"time"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/fisaks/uhn/internal/modbus"
	"github.com/fisaks/uhn/internal/uhn"
)

type SerialBusPoller struct {
	Bus     *config.BusConfig
	Devices []*config.DeviceConfig // devices on this bus (in order)

	PollPeriod time.Duration

	cmdCh      chan uhn.DeviceCommand
	pollCh     chan ZeroSignal
	cmdBufSize int

	client        DeviceClient
	scheduler     CommandScheduler
	edgePublisher uhn.EdgePublisher
}

type BusPoller interface {
	uhn.CommandPusher
	StartPoller(ctx context.Context) // starts the polling worker
	StopPoller()
	GetDevices() []*config.DeviceConfig
	GetBusConfig() *config.BusConfig
}
type PollResult struct {
	State     uhn.DeviceState
	Errors    []string
	Partial   bool
	AllFailed bool
}

func NewSerialBusPoller(bus *config.BusConfig, edgePublisher uhn.EdgePublisher) (BusPoller, error) {

	var deviceClient DeviceClient
	switch bus.Type {

	case "rtu":
		deviceClient = modbus.NewRTUDeviceClient(bus)
	case "tcp":
		deviceClient = modbus.NewTCPDeviceClient(bus)
	default:
		return nil, fmt.Errorf("unsupported bus type: %s", bus.Type)
	}

	pollPeriod := time.Duration(bus.PollIntervalMs) * time.Millisecond

	poller := SerialBusPoller{
		Bus:     bus,
		Devices: bus.Devices,

		PollPeriod: pollPeriod,

		cmdBufSize:    bus.CommandBufferSize,
		client:        deviceClient,
		edgePublisher: edgePublisher,
		cmdCh:         make(chan uhn.DeviceCommand, bus.CommandBufferSize),
		pollCh:        make(chan ZeroSignal, 1),
	}
	poller.scheduler = NewCommandScheduler(&poller)
	return &poller, nil

}

func (p *SerialBusPoller) StartPoller(ctx context.Context) {
	go func() {
		t := time.NewTicker(p.PollPeriod)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				logging.Info("BusPoller ctx done", "bus", p.Bus.BusId)
				// tickler stopped by defer t.Stop()
				return
			case <-t.C:
				select {
				case p.pollCh <- Zero: // send a signal; drop if one is queued
				default:
				}
			}
		}
	}()
	var address string
	if p.Bus.Type == "rtu" {
		address = p.Bus.Port
	} else if p.Bus.Type == "tcp" {
		address = p.Bus.TCPAddr
	}
	logging.Info("BusPoller started", "bus", p.Bus.BusId, "address", address, "type", p.Bus.Type, "poll", p.PollPeriod.Milliseconds(), "devices", len(p.Devices))
	p.poller(ctx)

}
func (p *SerialBusPoller) StopPoller() {
	p.scheduler.Stop()
	p.client.Close()
	logging.Info("BusPoller stopped", "bus", p.Bus.BusId)

}

func (p *SerialBusPoller) GetDevices() []*config.DeviceConfig {
	return p.Devices
}
func (p *SerialBusPoller) GetBusConfig() *config.BusConfig {
	return p.Bus
}

func (p *SerialBusPoller) poller(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			p.StopPoller()
			return
		case cmd := <-p.cmdCh:
			p.handleCommand(ctx, cmd)
		case <-p.pollCh:
			p.pollOnce(ctx) // your existing poll method
		}
	}
}

func (p *SerialBusPoller) pollOnce(ctx context.Context) {

	for _, device := range p.Devices {
		ok, pollResult := p.tryPollDevice(ctx, device)
		if !ok {
			logging.Warn("Poll failed", "bus", p.Bus.BusId, "device", device.Name, "errors", pollResult.Errors)
		}

		err := p.edgePublisher.PublishDeviceState(ctx, pollResult.State)
		if err != nil {
			logging.Warn("Failed to publish state", "bus", p.Bus.BusId, "device", device.Name, "error", err)
		}
	}
}
func (p *SerialBusPoller) tryPollDevice(ctx context.Context, device *config.DeviceConfig) (bool, PollResult) {
	state := uhn.DeviceState{
		Timestamp: time.Now(),
		Name:      device.Name,
		Status:    "ok",
	}
	var errors []string
	successfulReads := 0
	failedFCs := 0
	// ===== FC1: Coils (Digital Outputs) =====
	if r := device.CatalogSpec.DigitalOutputs; r != nil && r.Count > 0 {
		data, err := p.client.ReadDeviceDigitalOutput(ctx, device)
		if err != nil {
			logging.Error("Error reading digital outputs", "bus", p.Bus.BusId, "device", device.Name, "error", err)
			errors = append(errors, "digitalOutputs: "+err.Error())
			failedFCs++
		} else {
			state.DigitalOutputs = data
			successfulReads++
		}
	}
	// ===== FC2: Discrete Inputs (Digital Inputs) =====
	if r := device.CatalogSpec.DigitalInputs; r != nil && r.Count > 0 {
		data, err := p.client.ReadDeviceDigitalInput(ctx, device)
		if err != nil {
			logging.Error("Error reading digital inputs", "bus", p.Bus.BusId, "device", device.Name, "error", err)
			errors = append(errors, "digitalInputs: "+err.Error())
			failedFCs++
		} else {
			state.DigitalInputs = data
			successfulReads++
		}
	}

	// ===== FC3: Holding Registers (Analog Outputs) =====
	if r := device.CatalogSpec.AnalogOutputs; r != nil && r.Count > 0 {
		data, err := p.client.ReadDeviceAnalogOutput(ctx, device)
		if err != nil {
			logging.Error("Error reading analog outputs", "bus", p.Bus.BusId, "device", device.Name, "error", err)
			errors = append(errors, "analogOutputs: "+err.Error())
			failedFCs++
		} else {
			state.AnalogOutputs = data
			successfulReads++
		}
	}

	// ===== FC4: Input Registers (Analog Inputs) =====
	if r := device.CatalogSpec.AnalogInputs; r != nil && r.Count > 0 {
		data, err := p.client.ReadDeviceAnalogInput(ctx, device)
		if err != nil {
			logging.Error("Error reading analog inputs", "bus", p.Bus.BusId, "device", device.Name, "error", err)
			errors = append(errors, "analogInputs: "+err.Error())
			failedFCs++
		} else {
			state.AnalogInputs = data
			successfulReads++
		}
	}

	// Status
	state.Errors = errors
	if successfulReads == 0 {
		state.Status = "error"
	} else if failedFCs > 0 {
		state.Status = "partial_error"
	}

	return successfulReads > 0, PollResult{
		State:     state,
		Errors:    errors,
		Partial:   failedFCs > 0 && successfulReads > 0,
		AllFailed: successfulReads == 0,
	}

}
