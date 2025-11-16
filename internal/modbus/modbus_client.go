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

// TCP doesn’t have .Close(), so wrap it:
type TCPHandlerWithClose struct {
	*modbus.TCPClientHandler
}

func (h *TCPHandlerWithClose) Close() error {
	// TCP doesn't need explicit close; safe no-op
	return nil
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

	m.client = modbus.NewClient(m.handler)
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

// ===== FC1: Coils (Digital Outputs) =====
func (m *ModbusDeviceClient) ReadDeviceDigitalOutput(ctx context.Context, device *config.DeviceConfig) ([]byte, error) {
	deviceSpec := device.CatalogSpec
	deviceRange := deviceSpec.DigitalOutputs
	deviceLimits := deviceSpec.Limits

	if deviceLimits.MaxDigitalChunkSize >= deviceRange.Count {
		return m.withClient(ctx, device, READ, func() ([]byte, error) {
			return m.client.ReadCoils(deviceRange.Start, deviceRange.Count)
		})
	}

	data, err := m.readDeviceDigitalBitsChunked(ctx, device, deviceRange.Start, deviceRange.Count, deviceLimits.MaxDigitalChunkSize,
		func(addr, qty uint16) ([]byte, error) {
			return m.withClient(ctx, device, READ, func() ([]byte, error) { return m.client.ReadCoils(addr, qty) })
		})

	return data, err
}

// ===== FC2: Discrete Inputs (Digital Inputs) =====
func (m *ModbusDeviceClient) ReadDeviceDigitalInput(ctx context.Context, device *config.DeviceConfig) ([]byte, error) {
	deviceSpec := device.CatalogSpec
	deviceRange := deviceSpec.DigitalInputs
	deviceLimits := deviceSpec.Limits

	if deviceLimits.MaxDigitalChunkSize >= deviceRange.Count {
		return m.withClient(ctx, device, READ, func() ([]byte, error) {
			return m.client.ReadDiscreteInputs(deviceRange.Start, deviceRange.Count)
		})
	}

	data, err := m.readDeviceDigitalBitsChunked(ctx, device, deviceRange.Start, deviceRange.Count, deviceLimits.MaxDigitalChunkSize,
		func(addr, qty uint16) ([]byte, error) {
			return m.withClient(ctx, device, READ, func() ([]byte, error) { return m.client.ReadDiscreteInputs(addr, qty) })
		})

	return data, err
}

// ===== FC3: Holding Registers (Analog Outputs) =====
func (m *ModbusDeviceClient) ReadDeviceAnalogOutput(ctx context.Context, device *config.DeviceConfig) ([]byte, error) {
	deviceSpec := device.CatalogSpec
	deviceRange := deviceSpec.AnalogOutputs
	deviceLimits := deviceSpec.Limits

	if deviceLimits.MaxAnalogChunkSize >= deviceRange.Count {
		return m.withClient(ctx, device, READ, func() ([]byte, error) {
			return m.client.ReadHoldingRegisters(deviceRange.Start, deviceRange.Count)
		})
	}

	data, err := m.readDeviceAnalogWordsChunked(ctx, device, deviceRange.Start, deviceRange.Count, deviceLimits.MaxAnalogChunkSize,
		func(addr, qty uint16) ([]byte, error) {
			return m.withClient(ctx, device, READ, func() ([]byte, error) { return m.client.ReadHoldingRegisters(addr, qty) })
		})

	return data, err
}

// ===== FC4: Input Registers (Analog Inputs) =====
func (m *ModbusDeviceClient) ReadDeviceAnalogInput(ctx context.Context, device *config.DeviceConfig) ([]byte, error) {
	deviceSpec := device.CatalogSpec
	deviceRange := deviceSpec.AnalogInputs
	deviceLimits := deviceSpec.Limits

	if deviceLimits.MaxAnalogChunkSize >= deviceRange.Count {
		return m.withClient(ctx, device, READ, func() ([]byte, error) {
			return m.client.ReadInputRegisters(deviceRange.Start, deviceRange.Count)
		})
	}

	data, err := m.readDeviceAnalogWordsChunked(ctx, device, deviceRange.Start, deviceRange.Count, deviceLimits.MaxAnalogChunkSize,
		func(addr, qty uint16) ([]byte, error) {
			return m.withClient(ctx, device, READ, func() ([]byte, error) { return m.client.ReadInputRegisters(addr, qty) })
		})

	return data, err
}

func (m *ModbusDeviceClient) readDeviceDigitalBitsChunked(
	ctx context.Context,
	device *config.DeviceConfig,
	start, count uint16,
	chunkSize uint16,
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

	forEachChunk(start, count, chunkSize, func(addr, qty uint16) bool {
		data, err := readFn(addr, qty)
		if err != nil {
			logging.Error("read bits failed", "bus", m.busId, "addr", addr, "qty", qty, "error", err)
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
func (m *ModbusDeviceClient) readDeviceAnalogWordsChunked(
	ctx context.Context,
	device *config.DeviceConfig,
	start, count uint16,
	chunkSize uint16,
	readFn func(addr, qty uint16) ([]byte, error),
) ([]byte, error) {

	if count == 0 {
		return []byte{}, nil
	}
	// Each register = 2 bytes
	buf := make([]byte, 0, int(count)*2)

	var firstErr error

	forEachChunk(start, count, chunkSize, func(addr, qty uint16) bool {

		data, err := readFn(addr, qty)
		if err != nil {
			logging.Error("read regs failed", "bus", m.busId, "addr", addr, "qty", qty, "error", err)
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

// forEachChunk splits [start, start+total) into chunks of size <= chunkSize.
// The callback returns false to abort early; true to continue.
func forEachChunk(start, total, chunkSize uint16, fn func(addr, qty uint16) bool) {
	if total == 0 || chunkSize == 0 {
		return
	}
	left := total
	addr := start
	for left > 0 {
		step := minU16(left, chunkSize)
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
