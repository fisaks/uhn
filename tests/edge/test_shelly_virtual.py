"""Tests for the Shelly Pro 3EM virtual device profile.

These tests verify the simulator's Shelly profile produces realistic
energy meter readings matching the real Shelly Pro 3EM register layout.

Register spec: https://shelly-api-docs.shelly.cloud/gen2/ComponentsAndServices/EM/
"""
import time
from common.shelly_decoder import decode_float32_be, decode_all_phases


def _get_regs(sim_client, energy_meter) -> list[int]:
    """Fetch the analog input registers for the energy meter device."""
    state = sim_client.get_device_state(energy_meter.bus, energy_meter.name)
    regs = state["analogInputs"]
    assert regs is not None and len(regs) >= 75, f"Expected 75 input registers, got {len(regs) if regs else 0}"
    return regs


def test_voltage_in_range(sim_client, energy_meter):
    """All three phase voltages should be between 220V and 240V."""
    time.sleep(1.5)
    regs = _get_regs(sim_client, energy_meter)
    data = decode_all_phases(regs)

    for phase in ["a", "b", "c"]:
        v = data[f"phase_{phase}"]["voltage"]
        assert 220.0 <= v <= 240.0, f"Phase {phase} voltage {v} out of range [220, 240]"


def test_current_in_range(sim_client, energy_meter):
    """All three phase currents should be between 2A and 5A."""
    time.sleep(1.5)
    regs = _get_regs(sim_client, energy_meter)
    data = decode_all_phases(regs)

    for phase in ["a", "b", "c"]:
        c = data[f"phase_{phase}"]["current"]
        assert 2.0 <= c <= 5.0, f"Phase {phase} current {c} out of range [2, 5]"


def test_power_equals_voltage_times_current(sim_client, energy_meter):
    """Active power = V * I for each phase (within tolerance)."""
    time.sleep(1.5)
    regs = _get_regs(sim_client, energy_meter)
    data = decode_all_phases(regs)

    for phase in ["a", "b", "c"]:
        ph = data[f"phase_{phase}"]
        expected = ph["voltage"] * ph["current"]
        assert abs(ph["active_power"] - expected) < 0.5, (
            f"Phase {phase}: active_power={ph['active_power']}, "
            f"expected V*I={expected} (V={ph['voltage']}, I={ph['current']})"
        )


def test_total_power_is_sum_of_phases(sim_client, energy_meter):
    """Total active power should equal the sum of all three phase powers."""
    time.sleep(1.5)
    regs = _get_regs(sim_client, energy_meter)
    data = decode_all_phases(regs)

    phase_sum = sum(data[f"phase_{p}"]["active_power"] for p in ["a", "b", "c"])
    assert abs(data["total_active_power"] - phase_sum) < 1.0, (
        f"Total power {data['total_active_power']} != sum of phases {phase_sum}"
    )


def test_total_current_is_sum_of_phases(sim_client, energy_meter):
    """Total current should equal the sum of all three phase currents."""
    time.sleep(1.5)
    regs = _get_regs(sim_client, energy_meter)
    data = decode_all_phases(regs)

    phase_sum = sum(data[f"phase_{p}"]["current"] for p in ["a", "b", "c"])
    assert abs(data["total_current"] - phase_sum) < 0.1, (
        f"Total current {data['total_current']} != sum of phases {phase_sum}"
    )


def test_frequency_is_50hz(sim_client, energy_meter):
    """All phase frequencies should be 50 Hz (EU grid)."""
    time.sleep(1.5)
    regs = _get_regs(sim_client, energy_meter)
    data = decode_all_phases(regs)

    for phase in ["a", "b", "c"]:
        freq = data[f"phase_{phase}"]["frequency"]
        assert abs(freq - 50.0) < 0.1, f"Phase {phase} frequency {freq} != 50 Hz"


def test_power_factor_is_one(sim_client, energy_meter):
    """Power factor should be ~1.0 (simplified resistive load simulation)."""
    time.sleep(1.5)
    regs = _get_regs(sim_client, energy_meter)
    data = decode_all_phases(regs)

    for phase in ["a", "b", "c"]:
        pf = data[f"phase_{phase}"]["power_factor"]
        assert abs(pf - 1.0) < 0.01, f"Phase {phase} power factor {pf} != 1.0"


def test_float32_round_trip(sim_client, energy_meter):
    """Verify that setting an analog input register and reading it back preserves the float32 value."""
    import struct

    test_value = 231.5
    bits = struct.unpack(">I", struct.pack(">f", test_value))[0]
    high = (bits >> 16) & 0xFFFF
    low = bits & 0xFFFF

    # Write to register pair at offset 0 (within the analog input range)
    sim_client.set_analog_input(energy_meter.bus, energy_meter.name, 0, high)
    sim_client.set_analog_input(energy_meter.bus, energy_meter.name, 1, low)

    state = sim_client.get_device_state(energy_meter.bus, energy_meter.name)
    regs = state["analogInputs"]
    result = decode_float32_be(regs, 0)
    assert abs(result - test_value) < 0.01, f"Round-trip failed: {result} != {test_value}"
