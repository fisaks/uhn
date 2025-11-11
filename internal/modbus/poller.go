package modbus

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/goburrow/modbus"
)

type Publisher struct {
	Client      mqtt.Client
	TopicPrefix string // e.g. "uhn/edge1"
}
type DeviceState struct {
	Timestamp time.Time `json:"timestamp"`
	Name      string    `json:"name"`

	// Digital outputs (Modbus "Coils")
	//Array of bytes, each bit = 1 coil (digital output).
	//Example: Relay state, output pin value.
	DigitalOutputs []byte `json:"digitalOutputs,omitempty"`

	// Digital inputs (Modbus "Discrete Inputs")
	//Array of bytes, each bit = 1 input (digital input).
	//Example: Button press, sensor on/off.
	DigitalInputs []byte `json:"digitalInputs,omitempty"`

	// Writable 16-bit registers (Modbus "Holding Registers")
	//Array of bytes, interpreted as 16-bit registers.
	//Example: Write set point to register 100, or read/write analog value.
	AnalogOutputs []byte `json:"analogOutputs,omitempty"`

	// Read-only 16-bit registers (Modbus "Input Registers")
	//Array of bytes, interpreted as 16-bit registers, read-only.
	//Example: Measured temperature, analog input, sensor reading.
	AnalogInputs []byte `json:"analogInputs,omitempty"`

	// "ok", "error", "partial_error"
	Status string   `json:"status"`
	Errors []string `json:"errors,omitempty"`
}

type BusPoller struct {
	Bus               config.BusConfig
	Devices           []config.DeviceConfig // devices on this bus (in order)
	Catalog           map[string]config.CatalogDeviceSpec
	PollPeriod        time.Duration
	HeartbeatInterval time.Duration
	Publisher         *Publisher
	lastPublished     map[string]DeviceState // key = device.Name
	lastHeartbeat     map[string]time.Time   // key = device.Name

	// Reused Modbus connection (one per bus)
	rtuHandler *modbus.RTUClientHandler
	tcpHandler *modbus.TCPClientHandler
	client     modbus.Client

	// Connection and backoff state
	connOK      bool
	backoff     time.Duration
	backoffMin  time.Duration
	backoffMax  time.Duration
	lastConnErr error

	cmdCh      chan DeviceCommand
	pollCh     chan ZeroSignal
	cmdBufSize int

	timersMu sync.Mutex
	timers   map[string]*time.Timer
}

func timerKey(deviceName string, address uint16) string {
	return deviceName + ":" + strconv.Itoa(int(address))
}

// internal/poller/poller.go (constructor part)
func NewBusPollers(cfg *config.EdgeConfig, publisher *Publisher) (map[string]*BusPoller, error) {
	res := make(map[string]*BusPoller, len(cfg.Buses))
	for _, bus := range cfg.Buses {
		devices := cfg.Devices[bus.BusId] // now comes from the map
		pollPeriod := time.Duration(bus.PollIntervalMs) * time.Millisecond

		res[bus.BusId] = &BusPoller{
			Bus:               bus,
			Devices:           devices,
			Catalog:           cfg.Catalog,
			PollPeriod:        pollPeriod,
			HeartbeatInterval: time.Duration(cfg.HeartbeatInterval) * time.Second,
			Publisher:         publisher,
			lastPublished:     make(map[string]DeviceState),
			lastHeartbeat:     make(map[string]time.Time),
			backoffMin:        200 * time.Millisecond,
			backoffMax:        5 * time.Second,
			backoff:           0, // means "ready to try now"
			cmdBufSize:        cfg.CommandBufferSize,
			timers:            make(map[string]*time.Timer),
		}

	}
	return res, nil
}

/* =======
   Runner
   ======= */

func (p *BusPoller) Run(ctx context.Context) {

	if p.cmdCh == nil {
		p.cmdCh = make(chan DeviceCommand, p.cmdBufSize)
	}
	if p.pollCh == nil {
		p.pollCh = make(chan ZeroSignal, 1)
	}

	go func() {
		t := time.NewTicker(p.PollPeriod)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				logging.Info("BusPoller ctx done", "bus", p.Bus.BusId)
				return
			case <-t.C:
				select {
				case p.pollCh <- Zero: // send a signal; drop if one is queued
				default:
				}
			}
		}
	}()
	logging.Info("BusPoller worker", "bus", p.Bus.BusId, "type", p.Bus.Type, "poll", p.PollPeriod.Milliseconds(), "devices", len(p.Devices))
	p.worker(ctx)
}

func (p *BusPoller) worker(ctx context.Context) {
	for {
		// If a command is waiting, take it immediately.
		select {
		case <-ctx.Done():
			p.closeClient()
			return
		case cmd := <-p.cmdCh:
			p.handleCommand(ctx, cmd)
			continue
		default:
		}

		// Otherwise block; commands still win due to first select above.
		select {
		case <-ctx.Done():
			p.closeClient()
			return
		case cmd := <-p.cmdCh:
			p.handleCommand(ctx, cmd)
		case <-p.pollCh:
			p.pollOnce(ctx) // your existing poll method
		}
	}
}

/* ==========
   Poll Logic
   ========== */

type PollResult struct {
	State     DeviceState
	Errors    []string
	Partial   bool
	AllFailed bool
}

func (p *BusPoller) pollOnce(ctx context.Context) {

	now := time.Now()
	for _, device := range p.Devices {
		ok, pollResult := p.tryPollDevice(ctx, device)
		prevResult, hasPrev := p.lastPublished[device.Name]
		isChanged := !deviceStateEqual(prevResult, pollResult.State)
		needsHeartbeat := false
		if p.HeartbeatInterval > 0 {
			needsHeartbeat = !hasPrev || now.Sub(p.lastHeartbeat[device.Name]) > p.HeartbeatInterval
		}

		if isChanged || needsHeartbeat {
			p.publishDeviceState(device.Name, pollResult.State, true)
			p.lastPublished[device.Name] = pollResult.State
			p.lastHeartbeat[device.Name] = now
		}

		if !ok {
			logging.Warn("Poll failed", "bus", p.Bus.BusId, "device", device.Name, "errors", pollResult.Errors)
		}

	}
}

/*
==========================

	Poll a single device

==========================
*/
func (p *BusPoller) tryPollDevice(ctx context.Context, device config.DeviceConfig) (bool, PollResult) {
	deviceSpec, _ := p.getDeviceSpec(device)

	if err := p.ensureConnected(ctx); err != nil {
		logging.Error("connect failed", "bus", p.Bus.BusId, "device", device.Name, "error", err)
		return false, PollResult{
			State: DeviceState{
				Timestamp: time.Now(),
				Name:      device.Name,
				Status:    "error",
				Errors:    []string{fmt.Sprintf("connect: %v", err)},
			},
			AllFailed: true,
		}
	}

	state := DeviceState{
		Timestamp: time.Now(),
		Name:      device.Name,
		Status:    "ok",
	}
	var errors []string
	successfulReads := 0
	failedFCs := 0

	// ===== FC1: Coils (Digital Outputs) =====
	if r := deviceSpec.DigitalOutputs; r != nil && r.Count > 0 {
		data, err := p.readDeviceDigitalOutput(ctx, device)
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
	if r := deviceSpec.DigitalInputs; r != nil && r.Count > 0 {
		data, err := p.readDeviceDigitalInput(ctx, device)
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
	if r := deviceSpec.AnalogOutputs; r != nil && r.Count > 0 {
		data, err := p.readDeviceAnalogOutput(ctx, device)
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
	if r := deviceSpec.AnalogInputs; r != nil && r.Count > 0 {
		data, err := p.readDeviceAnalogInput(ctx, device)
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

/* ========
   Utils
   ======== */

func maxDur(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func lower(s string) string { return strings.ToLower(s) }

func deviceStateEqual(a, b DeviceState) bool {
	return bytes.Equal(a.DigitalOutputs, b.DigitalOutputs) &&
		bytes.Equal(a.DigitalInputs, b.DigitalInputs) &&
		bytes.Equal(a.AnalogOutputs, b.AnalogOutputs) &&
		bytes.Equal(a.AnalogInputs, b.AnalogInputs) &&
		a.Status == b.Status
}

func (p *BusPoller) getDevice(ctx context.Context, deviceName string) *config.DeviceConfig {

	for _, device := range p.Devices {
		if device.Name == deviceName {
			return &device

		}
	}
	return nil
}
