package profiles

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
	"time"

	"github.com/womat/mbserver"
)

// Shelly Pro 3EM Modbus register layout.
// All addresses are input register offsets (Modbus 3xxxx notation = address xxxx).
// See: https://shelly-api-docs.shelly.cloud/gen2/ComponentsAndServices/EM/
const (
	shellyBase = 1000 // EM component base address

	// Global registers (relative to shellyBase)
	regTimestamp     = 0  // uint32
	regErrPhaseA     = 2  // boolean
	regErrPhaseB     = 3  // boolean
	regErrPhaseC     = 4  // boolean
	regErrNeutral    = 5  // boolean
	regErrPhaseSeq   = 6  // boolean
	regNeutralCur    = 7  // float32
	regNeutralMis    = 9  // boolean
	regNeutralOC     = 10 // boolean
	regTotalCurrent  = 11 // float32
	regTotalActPower = 13 // float32
	regTotalAppPower = 15 // float32

	// Per-phase offsets (each phase block = 20 registers)
	phaseABase = 20
	phaseBBase = 40
	phaseCBase = 60

	// Within a phase block (relative offset)
	phVoltage      = 0  // float32
	phCurrent      = 2  // float32
	phActivePower  = 4  // float32
	phApparentPow  = 6  // float32
	phPowerFactor  = 8  // float32
	phErrOverpower = 10 // boolean
	phErrOvervolt  = 11 // boolean
	phErrOvercur   = 12 // boolean
	phFrequency    = 13 // float32
)

// ShellyPro3EM simulates a 3-phase energy meter matching the real
// Shelly Pro 3EM Modbus register layout.
type ShellyPro3EM struct {
	voltages [3]float32
	currents [3]float32
}

func NewShellyPro3EM() *ShellyPro3EM {
	return &ShellyPro3EM{
		voltages: [3]float32{230.0, 230.0, 230.0},
		currents: [3]float32{3.5, 3.5, 3.5},
	}
}

func (s *ShellyPro3EM) Name() string { return "shelly.pro3em" }

func (s *ShellyPro3EM) Init(dev *mbserver.Device) {
	s.writeRegisters(dev)
}

func (s *ShellyPro3EM) Tick(dev *mbserver.Device) {
	// Random walk for voltages: 220-240V
	for i := range s.voltages {
		s.voltages[i] += (rand.Float32() - 0.5) * 2.0
		s.voltages[i] = clampf(s.voltages[i], 220.0, 240.0)
	}
	// Random walk for currents: 2-5A
	for i := range s.currents {
		s.currents[i] += (rand.Float32() - 0.5) * 0.5
		s.currents[i] = clampf(s.currents[i], 2.0, 5.0)
	}

	s.writeRegisters(dev)
}

func (s *ShellyPro3EM) writeRegisters(dev *mbserver.Device) {
	regs := dev.InputRegisters[shellyBase:]

	// Timestamp (uint32, seconds since epoch)
	now := uint32(time.Now().Unix())
	regs[regTimestamp] = uint16(now >> 16)
	regs[regTimestamp+1] = uint16(now & 0xFFFF)

	// Clear error flags
	regs[regErrPhaseA] = 0
	regs[regErrPhaseB] = 0
	regs[regErrPhaseC] = 0
	regs[regErrNeutral] = 0
	regs[regErrPhaseSeq] = 0
	regs[regNeutralMis] = 0
	regs[regNeutralOC] = 0

	phaseBases := [3]int{phaseABase, phaseBBase, phaseCBase}
	var totalCurrent, totalActivePower, totalApparentPower float32

	for i := 0; i < 3; i++ {
		v := s.voltages[i]
		c := s.currents[i]
		activePower := v * c
		apparentPower := activePower // simplified: PF ~1.0
		powerFactor := float32(1.0)
		if apparentPower > 0 {
			powerFactor = activePower / apparentPower
		}

		base := phaseBases[i]
		writeFloat32BE(regs[base+phVoltage:], v)
		writeFloat32BE(regs[base+phCurrent:], c)
		writeFloat32BE(regs[base+phActivePower:], activePower)
		writeFloat32BE(regs[base+phApparentPow:], apparentPower)
		writeFloat32BE(regs[base+phPowerFactor:], powerFactor)
		regs[base+phErrOverpower] = 0
		regs[base+phErrOvervolt] = 0
		regs[base+phErrOvercur] = 0
		writeFloat32BE(regs[base+phFrequency:], 50.0) // EU grid

		totalCurrent += c
		totalActivePower += activePower
		totalApparentPower += apparentPower
	}

	// Neutral current (simplified: 0 for balanced load)
	writeFloat32BE(regs[regNeutralCur:], 0.0)

	// Totals
	writeFloat32BE(regs[regTotalCurrent:], totalCurrent)
	writeFloat32BE(regs[regTotalActPower:], totalActivePower)
	writeFloat32BE(regs[regTotalAppPower:], totalApparentPower)
}

// writeFloat32BE writes a float32 as two uint16 registers in big-endian word order.
// High word at regs[0], low word at regs[1].
func writeFloat32BE(regs []uint16, val float32) {
	bits := math.Float32bits(val)
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], bits)
	regs[0] = binary.BigEndian.Uint16(buf[0:2]) // high word
	regs[1] = binary.BigEndian.Uint16(buf[2:4]) // low word
}

// ReadFloat32BE reads a float32 from two uint16 registers in big-endian word order.
func ReadFloat32BE(regs []uint16) float32 {
	var buf [4]byte
	binary.BigEndian.PutUint16(buf[0:2], regs[0])
	binary.BigEndian.PutUint16(buf[2:4], regs[1])
	bits := binary.BigEndian.Uint32(buf[:])
	return math.Float32frombits(bits)
}

func clampf(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
