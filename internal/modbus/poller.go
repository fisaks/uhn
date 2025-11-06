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

type Publisher struct {
	Client      mqtt.Client
	TopicPrefix string // e.g. "edge/dev1"
}
type DeviceState struct {
	Timestamp time.Time
	Name      string
	//Array of bytes, each bit = 1 coil (digital output).
	//Example: Relay state, output pin value.
	Coils []byte
	//Array of bytes, each bit = 1 input (digital input).
	//Example: Button press, sensor on/off.
	DiscreteInputs []byte
	//Array of bytes, interpreted as 16-bit registers.
	//Example: Write setpoint to register 100, or read/write analog value.
	HoldingRegs []byte
	//Array of bytes, interpreted as 16-bit registers, read-only.
	//Example: Measured temperature, analog input, sensor reading.
	InputRegs []byte
	Status    string // "ok", "error", etc.
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
			return
		case <-t.C:
			p.pollOnce(ctx)
		}
	}
}

type retryRequest struct {
	device   config.DeviceConfig
	attempt  int
	maxTries int
}

func (p *BusPoller) pollOnce(ctx context.Context) {

	if len(p.Devices) == 0 {
		logging.Warn("BusPoller no devices to poll", "bus", p.Bus.BusId)
		return
	}
	var retryList []retryRequest
	now := time.Now()
	for _, device := range p.Devices {
		maxTries := device.RetryCount
		if maxTries == 0 {
			maxTries = 1 // default: no retry
		}
		ok, pollResult := p.tryPollDevice(device)
		prev, hasPrev := p.lastPublished[device.Name]
		isChanged := !deviceStateEqual(prev, pollResult.State)
		needsHeartbeat := !hasPrev || now.Sub(p.lastHeartbeat[device.Name]) > p.HeartbeatInterval

		if isChanged || needsHeartbeat {
			p.publishDeviceState(device.Name, pollResult.State, true)
			p.lastPublished[device.Name] = pollResult.State
			p.lastHeartbeat[device.Name] = now
		}

		// Add to retry list if not ok and we can retry
		if !ok && maxTries > 1 {
			retryList = append(retryList, retryRequest{
				device:   device,
				attempt:  2,
				maxTries: maxTries,
			})
		}
	}
	// 2. Retry loop
	for len(retryList) > 0 {
		var nextRetryList []retryRequest
		for _, r := range retryList {
			ok, pollResult := p.tryPollDevice(r.device)
			prev := p.lastPublished[r.device.Name]
			isChanged := !deviceStateEqual(prev, pollResult.State)
			needsHeartbeat := now.Sub(p.lastHeartbeat[r.device.Name]) > p.HeartbeatInterval

			if isChanged || needsHeartbeat {
				p.publishDeviceState(r.device.Name, pollResult.State, true)
				p.lastPublished[r.device.Name] = pollResult.State
				p.lastHeartbeat[r.device.Name] = now
			}

			if !ok && r.attempt < r.maxTries {
				nextRetryList = append(nextRetryList, retryRequest{
					device:   r.device,
					attempt:  r.attempt + 1,
					maxTries: r.maxTries,
				})
			}
		}
		retryList = nextRetryList
	}
}

type PollResult struct {
	State     DeviceState
	Errors    []string
	Partial   bool
	AllFailed bool
}

// Returns: success, result (for publish/error handling)
func (p *BusPoller) tryPollDevice(device config.DeviceConfig) (bool, PollResult) {
	deviceSpec := p.Catalog[device.Type]
	cli, closeFn, err := p.newClient(device)
	if err != nil {
		logging.Error("BusPoller", "bus", p.Bus.BusId, "device", device.Name, "error", err)
		return false, PollResult{
			State: DeviceState{
				Timestamp: time.Now(),
				Name:      device.Name,
				Status:    "error",
			},
			Errors:    []string{fmt.Sprintf("connect: %v", err)},
			AllFailed: true,
		}
	}
	defer closeFn()

	state := DeviceState{
		Timestamp: time.Now(),
		Name:      device.Name,
		Status:    "ok",
	}
	var errors []string
	successfulReads := 0
	failedFCs := 0
	gap := maxDur(p.Bus.SettleBeforeRequest(), deviceSpec.Timings.SettleBeforeRequest())
	// Coils (FC1)
	if r := deviceSpec.Coils; r != nil && r.Count > 0 {
		bits, err := p.tryReadBits(cli.ReadCoils, r.Start, r.Count, deviceSpec.Limits.MaxCoilsPerRead, gap)
		if err != nil {
			errors = append(errors, "coils: "+err.Error())
			state.Coils = nil
			failedFCs++
		} else {
			state.Coils = bits
			successfulReads++
		}
	}

	// Discrete Inputs (FC2)
	if r := deviceSpec.DiscreteInputs; r != nil && r.Count > 0 {
		bits, err := p.tryReadBits(cli.ReadDiscreteInputs, r.Start, r.Count, deviceSpec.Limits.MaxInputsPerRead, gap)
		if err != nil {
			errors = append(errors, "discreteInputs: "+err.Error())
			state.DiscreteInputs = nil
			failedFCs++
		} else {
			state.DiscreteInputs = bits
			successfulReads++
		}
	}

	// Holding Registers (FC3)
	if r := deviceSpec.HoldingRegs; r != nil && r.Count > 0 {
		regs, err := p.tryReadRegs(cli.ReadHoldingRegisters, r.Start, r.Count, deviceSpec.Limits.MaxRegistersPerRead, gap)
		if err != nil {
			errors = append(errors, "holdingRegs: "+err.Error())
			state.HoldingRegs = nil
			failedFCs++
		} else {
			state.HoldingRegs = regs
			successfulReads++
		}
	}

	// Input Registers (FC4)
	if r := deviceSpec.InputRegs; r != nil && r.Count > 0 {
		regs, err := p.tryReadRegs(cli.ReadInputRegisters, r.Start, r.Count, deviceSpec.Limits.MaxRegistersPerRead, gap)
		if err != nil {
			errors = append(errors, "inputRegs: "+err.Error())
			state.InputRegs = nil
			failedFCs++
		} else {
			state.InputRegs = regs
			successfulReads++
		}
	}

	// Set status
	if successfulReads == 0 {
		state.Status = "error"
	} else if failedFCs > 0 {
		state.Status = "partial_error"
	} else {
		state.Status = "ok"
	}

	return successfulReads > 0, PollResult{
		State:     state,
		Errors:    errors,
		Partial:   failedFCs > 0 && successfulReads > 0,
		AllFailed: successfulReads == 0,
	}
}

/* ================
   Client creation
   ================ */

type Client interface {
	ReadCoils(address, quantity uint16) (results []byte, err error)
	ReadDiscreteInputs(address, quantity uint16) (results []byte, err error)
	ReadHoldingRegisters(address, quantity uint16) (results []byte, err error)
	ReadInputRegisters(address, quantity uint16) (results []byte, err error)
}

func (p *BusPoller) newClient(device config.DeviceConfig) (Client, func(), error) {
	// per-device timeout: overrides > catalog default > bus timeout
	deviceSpec := p.Catalog[device.Type]
	timeout := p.Bus.Timeout()
	if deviceSpec.Timings.Timeout() > 0 {
		timeout = deviceSpec.Timings.Timeout()
	}
	if device.Overrides != nil && device.Overrides.TimeoutMs != nil && *device.Overrides.TimeoutMs > 0 {
		timeout = time.Duration(*device.Overrides.TimeoutMs) * time.Millisecond
	}

	switch lower(p.Bus.Type) {
	case "tcp":
		h := modbus.NewTCPClientHandler(p.Bus.TCPAddr)
		h.Timeout = timeout
		h.SlaveId = device.UnitId
		if p.Bus.Debug || device.Debug || deviceSpec.Debug {
			logging.Info("BusPoller enabling TCP debug logging", "bus", p.Bus.BusId, "device", device.Name)

			h.Logger = logging.WrapSlog("bus", p.Bus.BusId, "device", device.Name)
		}

		if err := h.Connect(); err != nil {
			return nil, func() {}, fmt.Errorf("tcp connect: %w", err)
		}
		c := modbus.NewClient(h)
		return c, func() { _ = h.Close() }, nil

	case "rtu":
		h := modbus.NewRTUClientHandler(p.Bus.Port)
		h.BaudRate = p.Bus.Baud
		h.DataBits = p.Bus.DataBits
		h.Parity = p.Bus.Parity
		h.StopBits = p.Bus.StopBits
		h.Timeout = timeout
		h.SlaveId = device.UnitId
		if p.Bus.Debug || device.Debug || deviceSpec.Debug {
			logging.Info("BusPoller enabling RTU debug logging", "bus", p.Bus.BusId, "device", device.Name)
			h.Logger = logging.WrapSlog("bus", p.Bus.BusId, "device", device.Name)
		}
		if err := h.Connect(); err != nil {
			return nil, func() {}, fmt.Errorf("rtu connect: %w", err)
		}
		c := modbus.NewClient(h)
		return c, func() { _ = h.Close() }, nil
	default:
		return nil, func() {}, fmt.Errorf("unknown bus type %q", p.Bus.Type)
	}
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

func (p *BusPoller) tryReadBits(
	readFn func(addr, qty uint16) ([]byte, error),
	start, count uint16,
	maxPerReq int,
	gap time.Duration) ([]byte, error) {

	maxQ := clamp(maxPerReq, 1, 2000)
	buf := make([]byte, 0, count)
	var errResult error
	forEachChunk(start, count, uint16(maxQ), func(addr, qty uint16) {
		if gap > 0 {
			time.Sleep(gap)
		}
		data, err := readFn(addr, qty)
		if err != nil {
			errResult = err
			logging.Info("BusPoller read bits", "addr", addr, "qty", qty, "error", err)
			return
		}
		buf = append(buf, data...)
	})
	return buf, errResult
}

func (p *BusPoller) tryReadRegs(
	readFn func(addr, qty uint16) ([]byte, error),
	start, count uint16,
	maxPerReq int,
	gap time.Duration) ([]byte, error) {

	maxQ := clamp(maxPerReq, 1, 125)
	buf := make([]byte, 0, count*2) // Registers are 2 bytes each
	var errResult error
	forEachChunk(start, count, uint16(maxQ), func(addr, qty uint16) {
		if gap > 0 {
			time.Sleep(gap)
		}
		data, err := readFn(addr, qty)
		if err != nil {
			errResult = err
			logging.Info("BusPoller read regs", "addr", addr, "qty", qty, "error", err)
			return
		}
		buf = append(buf, data...)
	})
	return buf, errResult
}
func (p *BusPoller) readRegs(
	ctx context.Context,
	device config.DeviceConfig,
	gap time.Duration,
	start, count uint16,
	maxPerReq int,
	readFn func(addr, qty uint16) ([]byte, error),
) {
	maxQ := clamp(maxPerReq, 1, 125)
	forEachChunk(start, count, uint16(maxQ), func(addr, qty uint16) {
		if gap > 0 {
			time.Sleep(gap)
		}
		_, err := readFn(addr, qty)
		if err != nil {
			logging.Info("BusPoller read regs", "bus", p.Bus.BusId, "device", device.Name, "addr", addr, "qty", qty, "error", err)
			return
		}

	})
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

// split range [start, start+total) into chunks of size <= maxChunk
func forEachChunk(start, total, maxChunk uint16, fn func(addr, qty uint16)) {
	if total == 0 || maxChunk == 0 {
		return
	}
	left := total
	addr := start
	for left > 0 {
		step := minU16(left, maxChunk)
		fn(addr, step)
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

func clamp(v, lo, hi int) int {
	return int(math.Max(float64(lo), math.Min(float64(v), float64(hi))))
}

func deviceStateEqual(a, b DeviceState) bool {
	return bytes.Equal(a.Coils, b.Coils) &&
		bytes.Equal(a.DiscreteInputs, b.DiscreteInputs) &&
		bytes.Equal(a.HoldingRegs, b.HoldingRegs) &&
		bytes.Equal(a.InputRegs, b.InputRegs) &&
		a.Status == b.Status
}
