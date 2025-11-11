package modbus

import (
	"context"

	"github.com/fisaks/uhn/internal/config"
	"github.com/fisaks/uhn/internal/logging"
)

const (
	MIN_DIGITAL_BITS_PER_READ = uint16(1)
	MAX_DIGITAL_BITS_PER_READ = uint16(2000)
	MIN_ANALOG_WORDS_PER_READ = uint16(1)
	MAX_ANALOG_WORDS_PER_READ = uint16(125)
)

func (p *BusPoller) readDeviceDigitalBitsChunked(
	ctx context.Context,
	device config.DeviceConfig,
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
		data, err := p.withClient(ctx, device, READ, func() ([]byte, error) { return readFn(addr, qty) })
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
func (p *BusPoller) readDeviceAnalogWordsChunked(
	ctx context.Context,
	device config.DeviceConfig,
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

		data, err := p.withClient(ctx, device, READ, func() ([]byte, error) { return readFn(addr, qty) })
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
