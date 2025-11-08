package modbus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/goburrow/modbus"
)

const (
	MIN_BITS_PER_READ = 1
	MAX_BITS_PER_READ = 2000
	MIN_REGS_PER_READ = 1
	MAX_REGS_PER_READ = 125
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
}

// internal/poller/poller.go (constructor part)
func NewBusPollers(cfg *config.EdgeConfig, publisher *Publisher) (map[string]*BusPoller, error) {
	res := make(map[string]*BusPoller, len(cfg.Buses))
	for _, bus := range cfg.Buses {
		devices := cfg.Devices[bus.BusId] // now comes from the map
		pollPeriod := time.Duration(bus.PollIntervalMs) * time.Millisecond
		if bus.PollIntervalMs == 0 {
			pollPeriod = time.Duration(cfg.PollIntervalMs) * time.Millisecond
		}
		if pollPeriod <= 0 {
			pollPeriod = 1000 * time.Millisecond
		}

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
		}

	}
	return res, nil
}

/* =======
   Runner
   ======= */

func (p *BusPoller) Run(ctx context.Context) {

	t := time.NewTicker(p.PollPeriod)
	defer t.Stop()

	logging.Info("BusPoller", "bus", p.Bus.BusId, "type", p.Bus.Type, "poll", p.PollPeriod.Milliseconds(), "devices", len(p.Devices))

	for {
		select {
		case <-ctx.Done():
			logging.Info("BusPoller ctx done", "bus", p.Bus.BusId)
			p.closeClient()
			return
		case <-t.C:
			p.pollOnce(ctx)
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

	if len(p.Devices) == 0 {
		logging.Warn("BusPoller no devices to poll", "bus", p.Bus.BusId)
		return
	}

	now := time.Now()
	for _, device := range p.Devices {
		ok, pollResult := p.tryPollDevice(ctx, device)
		prev, hasPrev := p.lastPublished[device.Name]
		isChanged := !deviceStateEqual(prev, pollResult.State)
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
	deviceSpec, ok := p.Catalog[device.Type]
	if !ok {
		logging.Warn("Unknown device type", "type", device.Type)
	}

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

	p.setSlave(device.UnitId)

	state := DeviceState{
		Timestamp: time.Now(),
		Name:      device.Name,
		Status:    "ok",
	}
	var errors []string
	successfulReads := 0
	failedFCs := 0
	gap := maxDur(p.Bus.SettleBeforeRequest(), deviceSpec.Timings.SettleBeforeRequest())

	// ===== FC1: Coils (Digital Outputs) =====
	if r := deviceSpec.DigitalOutputs; r != nil && r.Count > 0 {
		maxQ := clamp(deviceSpec.Limits.MaxCoilsPerRead, MIN_BITS_PER_READ, MAX_BITS_PER_READ)
		data, err := p.tryReadBitsChunked(ctx, r.Start, r.Count, maxQ, gap, func(addr, qty uint16) ([]byte, error) {
			result, err := p.client.ReadCoils(addr, qty)
			if err != nil {
				logging.Error("Error reading digital outputs", "bus", p.Bus.BusId, "device", device.Name, "addr", addr, "qty", qty, "error", err)
			}
			return result, err
		})
		if err != nil {
			errors = append(errors, "digitalOutputs: "+err.Error())
			failedFCs++
		} else {
			state.DigitalOutputs = data
			successfulReads++
		}
	}
	// ===== FC2: Discrete Inputs (Digital Inputs) =====
	if r := deviceSpec.DigitalInputs; r != nil && r.Count > 0 {
		maxQ := clamp(deviceSpec.Limits.MaxInputsPerRead, MIN_BITS_PER_READ, MAX_BITS_PER_READ)
		data, err := p.tryReadBitsChunked(ctx, r.Start, r.Count, maxQ, gap, func(addr, qty uint16) ([]byte, error) {
			result, err := p.client.ReadDiscreteInputs(addr, qty)
			if err != nil {
				logging.Error("Error reading digital inputs", "bus", p.Bus.BusId, "device", device.Name, "addr", addr, "qty", qty, "error", err)
			}
			return result, err
		})
		if err != nil {
			errors = append(errors, "digitalInputs: "+err.Error())
			failedFCs++
		} else {
			state.DigitalInputs = data
			successfulReads++
		}
	}

	// ===== FC3: Holding Registers (Analog Outputs) =====
	if r := deviceSpec.AnalogOutputs; r != nil && r.Count > 0 {
		maxQ := clamp(deviceSpec.Limits.MaxRegistersPerRead, MIN_REGS_PER_READ, MAX_REGS_PER_READ)
		data, err := p.tryReadRegsChunked(ctx, r.Start, r.Count, maxQ, gap, func(addr, qty uint16) ([]byte, error) {
			result, err := p.client.ReadHoldingRegisters(addr, qty)
			if err != nil {
				logging.Error("Error reading analog outputs", "bus", p.Bus.BusId, "device", device.Name, "addr", addr, "qty", qty, "error", err)
			}
			return result, err
		})
		if err != nil {
			errors = append(errors, "analogOutputs: "+err.Error())
			failedFCs++
		} else {
			state.AnalogOutputs = data
			successfulReads++
		}
	}

	// ===== FC4: Input Registers (Analog Inputs) =====
	if r := deviceSpec.AnalogInputs; r != nil && r.Count > 0 {
		maxQ := clamp(deviceSpec.Limits.MaxRegistersPerRead, MIN_REGS_PER_READ, MAX_REGS_PER_READ)
		data, err := p.tryReadRegsChunked(ctx, r.Start, r.Count, maxQ, gap, func(addr, qty uint16) ([]byte, error) {
			result, err := p.client.ReadInputRegisters(addr, qty)
			if err != nil {
				logging.Error("Error reading analog inputs", "bus", p.Bus.BusId, "device", device.Name, "addr", addr, "qty", qty, "error", err)
			}
			return result, err
		})
		if err != nil {
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

/* ==============================
   Reusable connection management
   ============================== */

func (p *BusPoller) ensureConnected(ctx context.Context) error {
	if p.connOK {
		return nil
	}
	if p.backoff > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(p.backoff):
		}
	}

	p.closeClient() // cleanup any stale

	switch lower(p.Bus.Type) {
	case "rtu":
		h := modbus.NewRTUClientHandler(p.Bus.Port)
		h.BaudRate = p.Bus.Baud
		h.DataBits = p.Bus.DataBits
		h.Parity = p.Bus.Parity
		h.StopBits = p.Bus.StopBits
		h.Timeout = p.Bus.Timeout()
		if p.Bus.Debug {
			h.Logger = logging.WrapSlog("bus", p.Bus.BusId)
		}
		if err := h.Connect(); err != nil {
			p.bumpBackoff(err)
			return err
		}
		p.rtuHandler = h
		p.client = modbus.NewClient(h)

	case "tcp":
		h := modbus.NewTCPClientHandler(p.Bus.TCPAddr)
		h.Timeout = p.Bus.Timeout()
		if p.Bus.Debug {
			h.Logger = logging.WrapSlog("bus", p.Bus.BusId)
		}
		if err := h.Connect(); err != nil {
			p.bumpBackoff(err)
			return err
		}
		p.tcpHandler = h
		p.client = modbus.NewClient(h)

	default:
		return fmt.Errorf("unknown bus type %q", p.Bus.Type)
	}

	p.connOK = true
	p.backoff = 0
	p.lastConnErr = nil
	return nil
}

func (p *BusPoller) bumpBackoff(err error) {
	p.connOK = false
	p.lastConnErr = err
	if p.backoff == 0 {
		p.backoff = p.backoffMin
	} else {
		p.backoff *= 2
		if p.backoff > p.backoffMax {
			p.backoff = p.backoffMax
		}
	}
}

func (p *BusPoller) setSlave(id byte) {
	if p.rtuHandler != nil {
		p.rtuHandler.SlaveId = id
	}
	if p.tcpHandler != nil {
		p.tcpHandler.SlaveId = id
	}
}

func (p *BusPoller) closeClient() {
	if p.rtuHandler != nil {
		_ = p.rtuHandler.Close()
		p.rtuHandler = nil
	}
	if p.tcpHandler != nil {
		_ = p.tcpHandler.Close()
		p.tcpHandler = nil
	}
	p.connOK = false
}

/* ===========================
   Resilient read wrapper
   =========================== */

func withClientRead(ctx context.Context, p *BusPoller, fn func() ([]byte, error)) ([]byte, error) {
	if err := p.ensureConnected(ctx); err != nil {
		return nil, err
	}
	v, err := fn()
	if err == nil {
		return v, nil
	}

	if isTransient(err) {
		p.bumpBackoff(err)
		if err2 := p.ensureConnected(ctx); err2 == nil {
			return fn()
		}
	}
	return nil, err
}

func isTransient(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "connection") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "reset") ||
		strings.Contains(s, "closed") ||
		strings.Contains(s, "i/o") ||
		strings.Contains(s, "timeout") {
		return true
	}
	logging.Warn("BusPoller encountered error that may be transient", "error", err)
	return false
}

// Publish device state to MQTT
func (p *BusPoller) publishDeviceState(deviceName string, state DeviceState, retain bool) {
	topic := p.Publisher.TopicPrefix + "/device/" + deviceName + "/state"
	payload, err := json.Marshal(state)
	if err != nil {
		logging.Error("BusPoller marshal state", "bus", p.Bus.BusId, "device", deviceName, "error", err)
		return
	}
	token := p.Publisher.Client.Publish(topic, 0, retain, payload)
	token.Wait()
	if token.Error() != nil {
		logging.Error("BusPoller mqtt publish", "bus", p.Bus.BusId, "device", deviceName, "error", token.Error())
	}
}

/* ====================
   Chunked read helpers
   ==================== */

func (p *BusPoller) tryReadBitsChunked(
	ctx context.Context,
	start, count uint16,
	maxPerReq int,
	gap time.Duration,
	readFn func(addr, qty uint16) ([]byte, error),
) ([]byte, error) {

	if count == 0 {
		return []byte{}, nil
	}
	// The number of bytes needed to store 'count' bits, rounded up to a whole byte.
	// integer division (not float division) is used. so 8+7=15, 15/8 = 1
	capBytes := int(count+7) / 8
	buf := make([]byte, 0, capBytes)

	var firstErr error

	forEachChunk(start, count, uint16(clamp(maxPerReq, MIN_BITS_PER_READ, MAX_BITS_PER_READ)), func(addr, qty uint16) bool {
		// honor gap with ctx
		if gap > 0 {
			select {
			case <-ctx.Done():
				firstErr = ctx.Err()
				return false
			case <-time.After(gap):
			}
		}
		data, err := withClientRead(ctx, p, func() ([]byte, error) { return readFn(addr, qty) })
		if err != nil {
			logging.Info("read bits failed", "bus", p.Bus.BusId, "addr", addr, "qty", qty, "error", err)
			firstErr = err

			return false // stop on first failure
		}
		buf = append(buf, data...)

		return true
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return buf, nil
}

// tryReadRegsChunked reads holding/input registers in chunks and concatenates the raw bytes.
// start/count are in registers; maxPerReq is in registers.
func (p *BusPoller) tryReadRegsChunked(
	ctx context.Context,
	start, count uint16,
	maxPerReq int,
	gap time.Duration,
	readFn func(addr, qty uint16) ([]byte, error),
) ([]byte, error) {

	if count == 0 {
		return []byte{}, nil
	}
	// Each register = 2 bytes
	buf := make([]byte, 0, int(count)*2)

	var firstErr error

	forEachChunk(start, count, uint16(clamp(maxPerReq, MIN_REGS_PER_READ, MAX_REGS_PER_READ)), func(addr, qty uint16) bool {
		if gap > 0 {
			select {
			case <-ctx.Done():
				firstErr = ctx.Err()
				return false
			case <-time.After(gap):
			}
		}
		data, err := withClientRead(ctx, p, func() ([]byte, error) { return readFn(addr, qty) })
		if err != nil {
			logging.Info("read regs failed", "bus", p.Bus.BusId, "addr", addr, "qty", qty, "error", err)
			firstErr = err
			return false // stop on first failure
		}

		buf = append(buf, data...)

		return true
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return buf, nil
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

// forEachChunk splits [start, start+total) into chunks of size <= maxChunk.
// The callback returns false to abort early; true to continue.
func forEachChunk(start, total, maxChunk uint16, fn func(addr, qty uint16) bool) {
	if total == 0 || maxChunk == 0 {
		return
	}
	left := total
	addr := start
	for left > 0 {
		step := minU16(left, maxChunk)
		if !fn(addr, step) {
			return
		}
		addr += step
		left -= step
	}
}

func minU16(a, b uint16) uint16 {
	if a < b {
		return a
	}
	return b
}

func clamp(value, lowest, highest int) int {
	return int(math.Max(float64(lowest),
		math.Min(float64(value), float64(highest))))
}
