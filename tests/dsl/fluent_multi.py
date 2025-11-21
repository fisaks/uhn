# tests/dsl/fluent_multi.py
from __future__ import annotations
from collections import defaultdict
import time
from typing import Dict, List, Literal, Optional
from common.rtu_sim_client import RtuSimClient
from common.mqtt_watcher import MqttWatcher
from dsl.device_model import TestDevice
from common.types import Catalog, Command, DeviceCommand, DeviceState
from dsl.fluent_sequence import SequenceBuilder


class DeviceBuilder:
    """
    Builder to configure a single device's GIVEN / setup phase.
    Call .done() to return to the parent Scenario for further chaining.
    """

    def  __init__(self, scenario: Scenario, device: TestDevice):
        self.scenario = scenario
        self.device = device
        self._output_index: Optional[int] = None
        self._input_index: Optional[int] = None

    def and_output(self, index: int) -> "DeviceBuilder":
        self._output_index = index
        return self

    def and_input(self, index: int) -> "DeviceBuilder":
        """Select index for next is_on / is_off call."""
        self._input_index = index
        return self

    def is_on(self, timeout: float = 3.0) -> "DeviceBuilder":
        if self._input_index is not None:
            idx = int(self._input_index)
            self._input_index = None
            res=self.scenario.sim.set_digital_input(self.device.bus, self.device.name, idx,1)
            assert res["status"] == "ok", f"Failed to set simulator input {idx} to 1 error {res}"
            self.scenario._expected_initial_states[self.device.name].append(("input", idx, 1))
            return self
        elif self._output_index is not None:
            idx = int(self._output_index)
            self._output_index = None
            res=self.scenario.sim.set_digital_output(self.device.bus, self.device.name, idx, 1)
            assert res["status"] == "ok", f"Failed to set simulator output {idx} to 1 error {res}"
            self.scenario._expected_initial_states[self.device.name].append(("output", idx, 1))
            return self
        else:
            raise ValueError("No output or input index selected for is_on()")
    

    def is_off(self, timeout: float = 3.0) -> "DeviceBuilder":
        if self._input_index is not None:
            idx = int(self._input_index)
            self._input_index = None
            res=self.scenario.sim.set_digital_input(self.device.bus, self.device.name, idx, 0)
            assert res["status"] == "ok", f"Failed to set simulator input {idx} to 0 error {res}"
            self.scenario._expected_initial_states[self.device.name].append(("input", idx, 0))
            return self
        elif self._output_index is not None:
            idx = int(self._output_index)
            self._output_index = None
            res=self.scenario.sim.set_digital_output(self.device.bus, self.device.name, idx, 0)
            assert res["status"] == "ok", f"Failed to set simulator output {idx} to 0 error {res}"
            self.scenario._expected_initial_states[self.device.name].append(("output", idx, 0))
            return self
        else:
            raise ValueError("No output or input index selected for is_off()")

    def with_outputs(self, values: list[int]) -> "DeviceBuilder":
        """Set ALL digital outputs at once using the simulator PATCH endpoint."""
        values_rev=list(reversed(values))
        res = self.scenario.sim.patch_device(
            self.device.bus,
            self.device.name,
            {"digitalOutputs": values_rev},
        )
        assert res["status"] == "ok", f"Failed to set simulator outputs {values_rev}: {res}"

        for idx, val in enumerate(values_rev):
            self.scenario._expected_initial_states[self.device.name].append(("output", idx, val))

        return self


    def with_inputs(self, values: list[int]) -> "DeviceBuilder":
        """Set ALL digital inputs at once using the simulator PATCH endpoint."""
        values_rev=list(reversed(values))
        res = self.scenario.sim.patch_device(
            self.device.bus,
            self.device.name,
            {"digitalInputs": values_rev},
        )
        assert res["status"] == "ok", f"Failed to set simulator inputs {values_rev}: {res}"

        for idx, val in enumerate(values_rev):
            self.scenario._expected_initial_states[self.device.name].append(("input", idx, val))

        return self
    
    def done(self) -> "Scenario":
        #time.sleep(0.250)
        return self.scenario


class Scenario:
    """
    Multi-device fluent test scenario. Use given(sim, mqtt) to create one.
    """

    def __init__(self, sim: RtuSimClient, mqtt: MqttWatcher):
        self.sim = sim
        self.mqtt = mqtt
        self.devices: Dict[str, TestDevice] = {}
        self._last_cmd_id: Dict[str, str] = {}
        self._last_cmd_time: Dict[str, float] = {}
        self._last_state_msg: Dict[str, DeviceState] = {}
        self._device_messages: Dict[str, List[DeviceState]] = defaultdict(list)
        self._expected_initial_states: Dict[str, List[tuple]] = defaultdict(list)
        self._log_messages: bool = False
        self._catalog: Dict[str,  Catalog] = defaultdict(Catalog)

    # --- device management ---
    def device(self, device: TestDevice) -> DeviceBuilder:
        """Register or reference a device under test and return a DeviceBuilder for its setup."""
        self.devices[device.name] = device
        return DeviceBuilder(self, device)

    def resync(self) -> "Scenario":
        """Send a resync command """
        edge_names = set(device.edge_name for device in self.devices.values())

        for edge_name in edge_names:
            self.mqtt.publish(f"uhn/{edge_name}/cmd", Command(action="resync").to_dict())
            
        return self

    def log_state_messages(self,on: bool) -> "Scenario":
        self._log_messages = on
        return self

    def clear_message_log(self) -> "Scenario":
        """Clear all currently stored state messages."""
        self._device_messages.clear()
        return self

    def sequence(self) -> SequenceBuilder:
        return SequenceBuilder(self)

    def wait_for_expected_initial_states(self, timeout: float = 8.0) -> "Scenario":
        """Wait until all expected initial states have been observed via MQTT."""
        deadline = time.time() + timeout
        for device_name, states in self._expected_initial_states.items():
            for state_type, index, expected_value in states:
                if state_type == "output":
                    self.thenDeviceOutputBitIs(
                        device=self.devices[device_name],
                        index=index,
                        expected_bit=expected_value,
                        timeout=deadline - time.time(),
                    )
                elif state_type == "input":
                    self.thenDeviceInputBitIs(
                        device=self.devices[device_name],
                        index=index,
                        expected_bit=expected_value,
                        timeout=deadline - time.time(),
                    )
        return self
    def wait(self, seconds: float) -> "Scenario":
        """Wait for a specified number of seconds."""
        time.sleep(seconds)
        return self
    
    # --- when / actions ---
    def when_command(
        self,
        device: TestDevice,
        action: Literal["setdigitaloutput"],
        address: str|int,
        value: str|int,
        pulseMs: Optional[int] = None,
        cmd_id: Optional[str] = None,
    ) -> "Scenario":
        """
        Send a command to a specific device via MQTT. Records last cmd id for this device.
        Use this repeatedly to send multiple commands to multiple devices.
        """
        if device.name not in self.devices:
            # implicitly register device
            self.devices[device.name] = device

        topic = f"uhn/{device.edge_name}/device/{device.name}/cmd"
        cid = cmd_id if cmd_id is not None else None
        self._last_cmd_id[device.name] = cid
        self.mqtt.publish(topic, DeviceCommand(
            device=device.name,
            action=action,
            address=address,
            value=value,
            id=cid,
            pulseMs=pulseMs,
        ).to_dict())
        return self

    def when_button_tapped(self, device: TestDevice,index: int) -> "Scenario":
        """Use RTU-sim press endpoint to simulate a tap on input."""
        if device.name not in self.devices:
            self.devices[device.name] = device

        self.sim.press_digital_input(device.bus, device.name, index, mode="tap")
        return self

    def when_button_pressed_short(self,device: TestDevice, index: int) -> "Scenario":
        """Use RTU-sim press endpoint to simulate a short press on input."""
        if device.name not in self.devices:
            self.devices[device.name] = device
        
        self.sim.press_digital_input(device.bus, device.name, index, mode="hold1")
        return self

    def when_button_pressed_long(self, device: TestDevice,   index: int) -> "Scenario":
        """Use RTU-sim press endpoint to simulate a press on input."""
        if device.name not in self.devices:
            self.devices[device.name] = device

        self.sim.press_digital_input(device.bus, device.name, index, mode="hold2")
        return self

    def when_input_toggled(self, device: TestDevice, index: int) -> "Scenario":
        """Toggle digital input at index."""
        if device.name not in self.devices:
            self.devices[device.name] = device

        res=self.sim.toggle_digital_input(bus_id=device.bus, device_name=device.name, index=index)
        assert res["status"] == "ok", f"Failed to toggle simulator input {index} error {res}"
        return self
    
    def when_output_toggled(self, device: TestDevice, index: int) -> "Scenario":
        """Toggle digital output at index."""
        if device.name not in self.devices:
            self.devices[device.name] = device

        res=self.sim.toggle_digital_output(bus_id=device.bus, device_name=device.name, index=index)
        assert res["status"] == "ok", f"Failed to toggle simulator output {index} error {res}"
        return self
    
    def when_input_is_set_on(self, device: TestDevice, index: int) -> "Scenario":
        """Toggle digital input at index."""
        if device.name not in self.devices:
            self.devices[device.name] = device

        res=self.sim.set_digital_input(bus_id=device.bus, device_name=device.name, index=index, value=1)
        assert res["status"] == "ok", f"Failed to set simulator input {index} to 1 error {res}"
        return self

    def when_input_is_set_off(self, device: TestDevice, index: int) -> "Scenario":
        """Toggle digital input at index."""
        if device.name not in self.devices:
            self.devices[device.name] = device

        res=self.sim.set_digital_input(bus_id=device.bus, device_name=  device.name, index=index, value=0)
        assert res["status"] == "ok", f"Failed to set simulator input {index} to 0 error {res}"
        return self

    def when_output_is_set_on(self, device: TestDevice, index: int) -> "Scenario":
        """Toggle digital output at index."""
        if device.name not in self.devices:
            self.devices[device.name] = device

        res=self.sim.set_digital_output(bus_id=device.bus, device_name=device.name, index=index, value=1)
        assert res["status"] == "ok", f"Failed to set simulator output {index} to 1 error {res}"
        return self

    def when_output_is_set_off(self, device: TestDevice, index: int) -> "Scenario":
        """Toggle digital output at index."""
        if device.name not in self.devices:
            self.devices[device.name] = device

        res=self.sim.set_digital_output(bus_id=device.bus, device_name=  device.name, index=index, value=0)
        assert res["status"] == "ok", f"Failed to set simulator output {index} to 0 error {res}"
        return self



    # --- then / assertions ---
    def _parse_topic(self, topic: str) -> Optional[tuple]:
        parts = topic.split("/")
        # expected form: ["uhn", "<edge>", "device", "<device>", "state"]
        if len(parts) >= 5 and parts[0] == "uhn" and parts[2] == "device" and parts[4] == "state":
            return "state",parts[1], parts[3]
        if(len(parts) >= 3 and parts[0] == "uhn" and parts[2] == "catalog"):
            return "catalog",parts[1], None
        return None
    
    def _route_incoming_messages_until(self, deadline: float) -> None:
        """
        Drain available messages into last-state and optional historical log until deadline.
        Always updates _last_state_msg for the device (most recent); only append into
        _device_messages if self._log_messages is True.
        """
        while True:
            remaining = deadline - time.time()
            if remaining <= 0:
                return
            try:
                topic, payload = self.mqtt.wait_for(timeout=min(remaining, 0.5))
            except TimeoutError:
                return
            parsed = self._parse_topic(topic)
            if parsed is None:
                continue
            type, edge, devname = parsed
            # store last state always (if payload is dict)
            if type == "state":
                state = DeviceState.from_dict(payload)
                self._last_state_msg[devname] = state
                if self._log_messages:
                    self._device_messages[devname].append(state)
            if type =="catalog":
                self._catalog[edge] = Catalog.from_dict(payload)

    

    def thenDeviceOutputBitIs(
        self,
        device: TestDevice,
        index: int,
        expected_bit: int,
        timeout: float = 8.0,
    ) -> "Scenario":
        """
        Check the most recent state message for `device` (i.e. _last_state_msg) and wait until
        it contains the expected output bit. This method does NOT scan historical logs; it uses
        the last-state view. If you need order/history checks, enable logging with log_state_messages(True).
        """
        deadline = time.time() + timeout
        device_name = device.name

        # quick helper to evaluate last state
        def _check_last() -> bool:
            device_state = self._last_state_msg.get(device_name)
            if device_state is None:
                return False
            
            if device_state.digitalOutputs is None:
                return False
            
            return device_state.output_bit(index) == expected_bit

        # check immediate
        if _check_last():
            return self

        # otherwise route incoming messages until we see the desired last-state
        while time.time() < deadline:
            self._route_incoming_messages_until(deadline)
            if _check_last():
                return self
            # small sleep to avoid tight loop (routes do blocking wait_for already)
            time.sleep(0.01)

        recent = self._device_messages.get(device_name, [])[-5:] if self._log_messages else self._last_state_msg.get(device_name)
        raise AssertionError(
            f"Timeout waiting for device {device.name} output bit {index} == {expected_bit}. "
            f"Last state: {recent}"
        )
    
    def thenDeviceOutputIs(
        self,
        device: TestDevice,
        expected: list[int],
        timeout: float = 8.0,
    ) -> "Scenario":

        deadline = time.time() + timeout
        device_name = device.name

        def _check_last() -> bool:
            st = self._last_state_msg.get(device_name)
            if st is None or st.digitalOutputs is None:
                return False
            return st.output_bits()[:len(expected)] == expected

        # immediate success?
        if _check_last():
            return self

        # otherwise wait
        while time.time() < deadline:
            self._route_incoming_messages_until(deadline)
            if _check_last():
                return self
            time.sleep(0.01)

        recent = (
            self._device_messages.get(device_name, [])[-5:]
            if self._log_messages
            else self._last_state_msg.get(device_name)
        )

        raise AssertionError(
            f"Timeout waiting for device {device_name} outputs == {expected}. "
            f"Last state: {recent}"
        )

    def thenDeviceInputBitIs(
        self,
        device: TestDevice,
        index: int,
        expected_bit: int,
        timeout: float = 8.0,
    ) -> "Scenario":
        """
        Check the most recent state message for `device` for the input bit.
        Uses _last_state_msg (not historical log).
        """
        deadline = time.time() + timeout
        device_name = device.name

        def _check_last_input() -> bool:
            device_state = self._last_state_msg.get(device_name)
            if device_state is None:
                return False
            
            if device_state.digitalInputs is None:
                return False
            
            return device_state.input_bit(index) == expected_bit

        if _check_last_input():
            return self

        while time.time() < deadline:
            self._route_incoming_messages_until(deadline)
            if _check_last_input():
                return self
            time.sleep(0.01)

        recent = self._device_messages.get(device_name, [])[-5:] if self._log_messages else self._last_state_msg.get(device_name)
        raise AssertionError(
            f"Timeout waiting for device {device.name} input bit {index} == {expected_bit}. "
            f"Last state: {recent}"
        )
    
    def thenDeviceInputIs(
        self,
        device: TestDevice,
        expected: list[int],
        timeout: float = 8.0,
    ) -> "Scenario":

        deadline = time.time() + timeout
        device_name = device.name

        def _check_last() -> bool:
            st = self._last_state_msg.get(device_name)
            if st is None or st.digitalInputs is None:
                return False
            return st.input_bits()[:len(expected)] == expected

        # immediate success?
        if _check_last():
            return self

        # otherwise wait
        while time.time() < deadline:
            self._route_incoming_messages_until(deadline)
            if _check_last():
                return self
            time.sleep(0.01)

        recent = (
            self._device_messages.get(device_name, [])[-5:]
            if self._log_messages
            else self._last_state_msg.get(device_name)
        )

        raise AssertionError(
            f"Timeout waiting for device {device_name} outputs == {expected}. "
            f"Last state: {recent}"
        )
       

# factory
def given( sim: RtuSimClient, mqtt: MqttWatcher) -> Scenario:
    return Scenario(sim, mqtt)
