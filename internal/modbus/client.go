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

/* ==============================
   Reusable connection management
   ============================== */

const READ = uint8(1)
const WRITE = uint8(2)

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
	p.stopAllTimers()
}

func (p *BusPoller) stopAllTimers() {
    p.timersMu.Lock()
    for k, t := range p.timers {
        if t != nil { t.Stop() }
        delete(p.timers, k)
    }
    p.timersMu.Unlock()
}

func (p *BusPoller) withClient(ctx context.Context, device config.DeviceConfig, access uint8, fn func() ([]byte, error)) ([]byte, error) {
	if err := p.ensureConnected(ctx); err != nil {
		return nil, err
	}
	deviceSpec, _ := p.getDeviceSpec(device)
	p.setSlave(device.UnitId)

	v, err := p.callClientFunctionWithSettle(ctx, deviceSpec, access, fn)

	if err == nil {
		return v, nil
	}
	logging.Warn("withClient error", "bus", p.Bus.BusId, "device", device.Name, "error", err, "retrying")
	if isTransient(err) {
		p.bumpBackoff(err)
		if err2 := p.ensureConnected(ctx); err2 == nil {
			return p.callClientFunctionWithSettle(ctx, deviceSpec, access, fn)
		}
	}
	return nil, err
}
func (p *BusPoller) callClientFunctionWithSettle(ctx context.Context, deviceSpec config.CatalogDeviceSpec, access uint8, fn func() ([]byte, error)) ([]byte, error) {
	if err := p.settleBeforeRequest(ctx, deviceSpec); err != nil {
		return nil, err
	}
	v, err := fn()
	if err == nil {
		if access == WRITE {
			if err := p.settleAfterWrite(ctx, deviceSpec); err != nil {
				return nil, err
			}
		}
		return v, nil
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

func (p *BusPoller) readSingleDigitalOutput(ctx context.Context,
	device config.DeviceConfig, addr uint16) (bool, error) {

	data, err := p.withClient(ctx, device, READ, func() ([]byte, error) {
		// FC1, qty=1 returns 1 byte; bit0 is the coil
		return p.client.ReadCoils(addr, 1)
	})
	if err != nil {
		return false, err
	}
	if len(data) == 0 {
		return false, fmt.Errorf("empty coil response")
	}
	return (data[0] & 0x01) != 0, nil
}

func (p *BusPoller) writeSingleDigitalOutput(ctx context.Context, device config.DeviceConfig, addr uint16, value uint16) error {

	_, err := p.withClient(ctx, device, WRITE, func() ([]byte, error) {
		return p.client.WriteSingleCoil(addr, value)
	})

	return err
}

// ===== FC1: Coils (Digital Outputs) =====
func (p *BusPoller) readDeviceDigitalOutput(ctx context.Context,
	device config.DeviceConfig) ([]byte, error) {

	deviceSpec, _ := p.getDeviceSpec(device)
	deviceRange := deviceSpec.DigitalOutputs
	deviceLimits := deviceSpec.Limits

	if deviceLimits.MaxDigitalChunkSize >= deviceRange.Count {
		return p.withClient(ctx, device, READ, func() ([]byte, error) {
			return p.client.ReadCoils(deviceRange.Start, deviceRange.Count)
		})
	}

	data, err := p.readDeviceDigitalBitsChunked(ctx, device, deviceRange.Start, deviceRange.Count, deviceLimits.MaxDigitalChunkSize,
		func(addr, qty uint16) ([]byte, error) {
			return p.client.ReadCoils(addr, qty)
		})

	return data, err
}

// ===== FC2: Discrete Inputs (Digital Inputs) =====
func (p *BusPoller) readDeviceDigitalInput(ctx context.Context,
	device config.DeviceConfig) ([]byte, error) {

	deviceSpec, _ := p.getDeviceSpec(device)
	deviceRange := deviceSpec.DigitalInputs
	deviceLimits := deviceSpec.Limits

	if deviceLimits.MaxDigitalChunkSize >= deviceRange.Count {
		return p.withClient(ctx, device, READ, func() ([]byte, error) {
			return p.client.ReadDiscreteInputs(deviceRange.Start, deviceRange.Count)
		})
	}

	data, err := p.readDeviceDigitalBitsChunked(ctx, device, deviceRange.Start, deviceRange.Count, deviceLimits.MaxDigitalChunkSize,
		func(addr, qty uint16) ([]byte, error) {
			return p.client.ReadDiscreteInputs(addr, qty)
		})

	return data, err
}

// ===== FC3: Holding Registers (Analog Outputs) =====
func (p *BusPoller) readDeviceAnalogOutput(ctx context.Context,
	device config.DeviceConfig) ([]byte, error) {
	deviceSpec, _ := p.getDeviceSpec(device)
	deviceRange := deviceSpec.AnalogOutputs
	deviceLimits := deviceSpec.Limits

	if deviceLimits.MaxAnalogChunkSize >= deviceRange.Count {
		return p.withClient(ctx, device, READ, func() ([]byte, error) {
			return p.client.ReadHoldingRegisters(deviceRange.Start, deviceRange.Count)
		})
	}

	data, err := p.readDeviceAnalogWordsChunked(ctx, device, deviceRange.Start, deviceRange.Count, deviceLimits.MaxAnalogChunkSize,
		func(addr, qty uint16) ([]byte, error) {
			return p.client.ReadHoldingRegisters(addr, qty)
		})

	return data, err
}

// ===== FC4: Input Registers (Analog Inputs) =====
func (p *BusPoller) readDeviceAnalogInput(ctx context.Context,
	device config.DeviceConfig) ([]byte, error) {
	deviceSpec, _ := p.getDeviceSpec(device)
	deviceRange := deviceSpec.AnalogInputs
	deviceLimits := deviceSpec.Limits

	if deviceLimits.MaxAnalogChunkSize >= deviceRange.Count {
		return p.withClient(ctx, device, READ, func() ([]byte, error) {
			return p.client.ReadInputRegisters(deviceRange.Start, deviceRange.Count)
		})
	}

	data, err := p.readDeviceAnalogWordsChunked(ctx, device, deviceRange.Start, deviceRange.Count, deviceLimits.MaxAnalogChunkSize,
		func(addr, qty uint16) ([]byte, error) {
			return p.client.ReadInputRegisters(addr, qty)
		})

	return data, err
}

func (p *BusPoller) settleBeforeRequest(ctx context.Context, deviceSpec config.CatalogDeviceSpec) error {
	if gap := firstNonZeroDur(deviceSpec.Timings.SettleBeforeRequest(), p.Bus.SettleBeforeRequest()); gap > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(gap):
		}
	}
	return nil
}
func (p *BusPoller) settleAfterWrite(ctx context.Context, deviceSpec config.CatalogDeviceSpec) error {
	if gap := firstNonZeroDur(deviceSpec.Timings.SettleAfterWrite(), p.Bus.SettleAfterWrite()); gap > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(gap):
		}
	}
	return nil
}

func (p *BusPoller) getDeviceSpec(device config.DeviceConfig) (config.CatalogDeviceSpec, bool) {
	deviceSpec, ok := p.Catalog[device.Type]

	return deviceSpec, ok
}

func firstNonZeroDur(durs ...time.Duration) time.Duration {
	for _, d := range durs {
		if d > 0 {
			return d
		}
	}
	return 0
}
