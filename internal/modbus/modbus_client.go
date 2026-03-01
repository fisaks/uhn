package modbus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
	"github.com/goburrow/modbus"
)

const (
	READ  = uint8(1)
	WRITE = uint8(2)
)

type ModbusHandler interface {
	modbus.ClientHandler
	Connect() error
	Close() error
}

type ModbusDeviceClient struct {
	handler ModbusHandler // This is an interface satisfied by both RTU and TCP handlers
	client  modbus.Client
	busId   string
	// Connection and backoff state
	connOK      bool
	backoff     time.Duration
	backoffMin  time.Duration
	backoffMax  time.Duration
	lastConnErr error
}

func newModbusDeviceClient(handler ModbusHandler, busId string) *ModbusDeviceClient {
	return &ModbusDeviceClient{
		handler:     handler,
		client:      modbus.NewClient(handler),
		busId:       busId,
		connOK:      true,
		backoff:     0, // means "ready to try now"
		backoffMin:  200 * time.Millisecond,
		backoffMax:  5 * time.Second,
		lastConnErr: nil,
	}
}
func NewRTUDeviceClient(bus *config.BusConfig) *ModbusDeviceClient {
	handler := modbus.NewRTUClientHandler(bus.Port)
	handler.BaudRate = bus.Baud
	handler.DataBits = bus.DataBits
	handler.Parity = bus.Parity
	handler.StopBits = bus.StopBits
	handler.Timeout = bus.Timeout()
	if bus.Debug {
		handler.Logger = logging.WrapSlog("bus", bus.BusId)
	}
	return newModbusDeviceClient(handler, bus.BusId)
}

func NewTCPDeviceClient(bus *config.BusConfig) *ModbusDeviceClient {
	handler := modbus.NewTCPClientHandler(bus.TCPAddr)
	handler.Timeout = bus.Timeout()

	if bus.Debug {
		handler.Logger = logging.WrapSlog("bus", bus.BusId)
	}
	return newModbusDeviceClient(handler, bus.BusId)
}

func (m *ModbusDeviceClient) EnsureConnected(ctx context.Context) error {
	if m.connOK {
		return nil
	}
	backoff := m.backoff

	if backoff > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}

	m.Close() // cleanup any stale
	if err := m.handler.Connect(); err != nil {
		m.bumpBackoff(err)
		return err
	}

	m.connOK = true
	m.backoff = 0
	m.lastConnErr = nil
	return nil
}

func (m *ModbusDeviceClient) Close() {
	m.handler.Close()
	m.connOK = false
}

func (m *ModbusDeviceClient) bumpBackoff(err error) {
	m.connOK = false
	m.lastConnErr = err
	if m.backoff == 0 {
		m.backoff = m.backoffMin
	} else {
		m.backoff *= 2
		if m.backoff > m.backoffMax {
			m.backoff = m.backoffMax
		}
	}
}
func (m *ModbusDeviceClient) setSlave(id byte) {
	switch h := m.handler.(type) {
	case *modbus.RTUClientHandler:
		h.SlaveId = id
	case *modbus.TCPClientHandler:
		h.SlaveId = id
	default:
		logging.Error("Unknown Modbus handler type", "type", fmt.Sprintf("%T", h))
	}
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

func (m *ModbusDeviceClient) withClient(ctx context.Context, device *config.DeviceConfig, access uint8, fn func() ([]byte, error)) ([]byte, error) {
	if err := m.EnsureConnected(ctx); err != nil {
		return nil, err
	}

	m.setSlave(device.UnitId)

	v, err := m.callClientFunctionWithSettle(ctx, device, access, fn)

	if err == nil {
		return v, nil
	}
	logging.Warn("withClient error", "bus", m.busId, "device", device.Name, "error", err, "retrying")
	if isTransient(err) {
		m.bumpBackoff(err)
		if err2 := m.EnsureConnected(ctx); err2 == nil {
			return m.callClientFunctionWithSettle(ctx, device, access, fn)
		}
	}
	return nil, err
}
func (m *ModbusDeviceClient) callClientFunctionWithSettle(ctx context.Context, device *config.DeviceConfig, access uint8, fn func() ([]byte, error)) ([]byte, error) {
	if err := m.settleBeforeRequest(ctx, device); err != nil {
		return nil, err
	}
	v, err := fn()
	if err == nil {
		if access == WRITE {
			if err := m.settleAfterWrite(ctx, device); err != nil {
				return nil, err
			}
		}
		return v, nil
	}
	return nil, err
}
func (m *ModbusDeviceClient) settleBeforeRequest(ctx context.Context, device *config.DeviceConfig) error {
	if gap := firstNonZeroDur(device.CatalogSpec.Timings.SettleBeforeRequest(), device.Bus.SettleBeforeRequest()); gap > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(gap):
		}
	}
	return nil
}
func (m *ModbusDeviceClient) settleAfterWrite(ctx context.Context, device *config.DeviceConfig) error {
	if gap := firstNonZeroDur(device.CatalogSpec.Timings.SettleAfterWrite(), device.Bus.SettleAfterWrite()); gap > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(gap):
		}
	}
	return nil
}
func firstNonZeroDur(durations ...time.Duration) time.Duration {
	for _, d := range durations {
		if d > 0 {
			return d
		}
	}
	return 0
}

func (m *ModbusDeviceClient) ReadSingleDigitalOutput(ctx context.Context,
	device *config.DeviceConfig, addr uint16) (bool, error) {

	data, err := m.withClient(ctx, device, READ, func() ([]byte, error) {
		// FC1, qty=1 returns 1 byte; bit0 is the coil
		return m.client.ReadCoils(addr, 1)
	})
	if err != nil {
		return false, err
	}
	if len(data) == 0 {
		return false, fmt.Errorf("empty coil response")
	}
	return (data[0] & 0x01) != 0, nil
}

func (m *ModbusDeviceClient) WriteSingleDigitalOutput(ctx context.Context, device *config.DeviceConfig, addr uint16, value bool) error {

	_, err := m.withClient(ctx, device, WRITE, func() ([]byte, error) {
		val := uint16(0)
		if value {
			val = 0xFF00
		}
		return m.client.WriteSingleCoil(addr, val)
	})

	return err
}
func (m *ModbusDeviceClient) ToggleSingleDigitalOutput(ctx context.Context, device *config.DeviceConfig, addr uint16) error {
	_, err := m.withClient(ctx, device, WRITE, func() ([]byte, error) {
		val := device.CatalogSpec.Capabilities.ToggleWord
		if val == 0 {
			coil, err := m.client.ReadCoils(addr, 1)
			if err != nil {
				return coil, err
			}
			if coil[0]&0x01 != 0 {
				val = 0x0000
			} else {
				val = 0xFF00
			}
		}

		return m.client.WriteSingleCoil(addr, val)
	})

	return err
}
func (m *ModbusDeviceClient) ReadSingleDigitalInput(ctx context.Context, device *config.DeviceConfig, addr uint16) (bool, error) {
	data, err := m.withClient(ctx, device, READ, func() ([]byte, error) {
		// FC1, qty=1 returns 1 byte; bit0 is the coil
		return m.client.ReadDiscreteInputs(addr, 1)
	})
	if err != nil {
		return false, err
	}
	if len(data) == 0 {
		return false, fmt.Errorf("empty discrete input response")
	}
	return (data[0] & 0x01) != 0, nil
}

func digitalCap(count uint16) int { return int(count+7) / 8 }
func analogCap(count uint16) int  { return int(count) * 2 }

func (m *ModbusDeviceClient) ReadDeviceDigitalOutput(ctx context.Context, device *config.DeviceConfig) ([]byte, error) {
	spec := device.CatalogSpec
	return m.readDeviceRange(ctx, device, spec.DigitalOutputs, spec.Limits.MaxDigitalChunkSize, digitalCap, m.client.ReadCoils)
}

func (m *ModbusDeviceClient) ReadDeviceDigitalInput(ctx context.Context, device *config.DeviceConfig) ([]byte, error) {
	spec := device.CatalogSpec
	return m.readDeviceRange(ctx, device, spec.DigitalInputs, spec.Limits.MaxDigitalChunkSize, digitalCap, m.client.ReadDiscreteInputs)
}

func (m *ModbusDeviceClient) WriteSingleAnalogOutput(ctx context.Context, device *config.DeviceConfig, addr uint16, value uint16) error {
	_, err := m.withClient(ctx, device, WRITE, func() ([]byte, error) {
		return m.client.WriteSingleRegister(addr, value)
	})
	return err
}

func (m *ModbusDeviceClient) ReadDeviceAnalogOutput(ctx context.Context, device *config.DeviceConfig) ([]byte, error) {
	spec := device.CatalogSpec
	return m.readDeviceRange(ctx, device, spec.AnalogOutputs, spec.Limits.MaxAnalogChunkSize, analogCap, m.client.ReadHoldingRegisters)
}

func (m *ModbusDeviceClient) ReadDeviceAnalogInput(ctx context.Context, device *config.DeviceConfig) ([]byte, error) {
	spec := device.CatalogSpec
	return m.readDeviceRange(ctx, device, spec.AnalogInputs, spec.Limits.MaxAnalogChunkSize, analogCap, m.client.ReadInputRegisters)
}

func (m *ModbusDeviceClient) readDeviceRange(
	ctx context.Context,
	device *config.DeviceConfig,
	deviceRange *config.Range,
	maxChunkSize uint16,
	capFn func(uint16) int,
	modbusRead func(uint16, uint16) ([]byte, error),
) ([]byte, error) {
	if maxChunkSize >= deviceRange.Count {
		return m.withClient(ctx, device, READ, func() ([]byte, error) {
			return modbusRead(deviceRange.Start, deviceRange.Count)
		})
	}
	return m.readChunked(deviceRange.Start, deviceRange.Count, maxChunkSize, capFn,
		func(addr, qty uint16) ([]byte, error) {
			return m.withClient(ctx, device, READ, func() ([]byte, error) {
				return modbusRead(addr, qty)
			})
		})
}

func (m *ModbusDeviceClient) readChunked(
	start, count, chunkSize uint16,
	capFn func(uint16) int,
	readFn func(addr, qty uint16) ([]byte, error),
) ([]byte, error) {
	if count == 0 {
		return []byte{}, nil
	}
	buf := make([]byte, 0, capFn(count))
	var firstErr error
	forEachChunk(start, count, chunkSize, func(addr, qty uint16) bool {
		data, err := readFn(addr, qty)
		if err != nil {
			logging.Error("read failed", "bus", m.busId, "addr", addr, "qty", qty, "error", err)
			firstErr = err
			return false
		}
		buf = append(buf, data...)
		return true
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return buf, nil
}

// forEachChunk splits [start, start+total) into chunks of size <= chunkSize.
// The callback returns false to abort early; true to continue.
func forEachChunk(start, total, chunkSize uint16, fn func(addr, qty uint16) bool) {
	if total == 0 || chunkSize == 0 {
		return
	}
	left := total
	addr := start
	for left > 0 {
		step := min(left, chunkSize)
		if !fn(addr, step) {
			return
		}
		addr += step
		left -= step
	}
}
