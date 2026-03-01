"""Decode Shelly Pro 3EM Modbus register values.

Register layout matches the real device:
https://shelly-api-docs.shelly.cloud/gen2/ComponentsAndServices/EM/

All floats are 32-bit, big-endian word order (high word first).
Input registers start at address 1000 (Modbus 31000).
"""
import struct

# Global register offsets (relative to base 1000)
REG_TIMESTAMP = 0        # uint32
REG_NEUTRAL_CUR = 7      # float32
REG_TOTAL_CURRENT = 11   # float32
REG_TOTAL_ACT_POWER = 13 # float32
REG_TOTAL_APP_POWER = 15 # float32

# Per-phase base offsets
PHASE_A_BASE = 20
PHASE_B_BASE = 40
PHASE_C_BASE = 60

# Within a phase block
PH_VOLTAGE = 0       # float32
PH_CURRENT = 2       # float32
PH_ACTIVE_POWER = 4  # float32
PH_APPARENT_POWER = 6 # float32
PH_POWER_FACTOR = 8  # float32
PH_FREQUENCY = 13    # float32


def decode_float32_be(regs: list[int], offset: int = 0) -> float:
    """Decode a float32 from two uint16 registers at `offset` (big-endian word order).

    regs[offset]   = high word
    regs[offset+1] = low word
    """
    high = regs[offset] & 0xFFFF
    low = regs[offset + 1] & 0xFFFF
    raw = (high << 16) | low
    return struct.unpack(">f", struct.pack(">I", raw))[0]


def decode_phase(regs: list[int], phase_base: int) -> dict:
    """Decode one phase's registers into a dict."""
    return {
        "voltage": decode_float32_be(regs, phase_base + PH_VOLTAGE),
        "current": decode_float32_be(regs, phase_base + PH_CURRENT),
        "active_power": decode_float32_be(regs, phase_base + PH_ACTIVE_POWER),
        "apparent_power": decode_float32_be(regs, phase_base + PH_APPARENT_POWER),
        "power_factor": decode_float32_be(regs, phase_base + PH_POWER_FACTOR),
        "frequency": decode_float32_be(regs, phase_base + PH_FREQUENCY),
    }


def decode_all_phases(regs: list[int]) -> dict:
    """Decode all 3 phases + totals from the 75 input registers.

    `regs` should be the 75 registers starting at address 1000.
    Returns dict with phase_a/b/c sub-dicts and total_* fields.
    """
    result = {}
    for label, base in [("a", PHASE_A_BASE), ("b", PHASE_B_BASE), ("c", PHASE_C_BASE)]:
        result[f"phase_{label}"] = decode_phase(regs, base)

    result["neutral_current"] = decode_float32_be(regs, REG_NEUTRAL_CUR)
    result["total_current"] = decode_float32_be(regs, REG_TOTAL_CURRENT)
    result["total_active_power"] = decode_float32_be(regs, REG_TOTAL_ACT_POWER)
    result["total_apparent_power"] = decode_float32_be(regs, REG_TOTAL_APP_POWER)
    return result
