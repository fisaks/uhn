from __future__ import annotations
from dataclasses import dataclass
from typing import List, Literal, Optional
from datetime import datetime
import time

from dsl.device_model import TestDevice


# helper: convert bytes -> int mask (LSB bit0)
def _bytes_to_mask_le(b: Optional[bytes]) -> int:
    if not b:
        return 0
    return int.from_bytes(b, byteorder="little", signed=False)

# --- typed small models instead of dict / Any ---

@dataclass(frozen=True)
class SequenceConstraint:
    type: Literal["min", "max", "between", "never"]
    min: Optional[float] = None
    max: Optional[float] = None
    forbidden_value: Optional[int] = None


@dataclass(frozen=True)
class SequenceStep:
    kind: Literal["input", "output"]
    device: TestDevice
    index: int
    value: int
    constraint: Optional[SequenceConstraint] = None

MICROSECOND = 1e-6

class SequenceBuilder:
    """
    Sequence builder that uses SequenceStep dataclasses (no Any).
    Usage:
        s.log_state_messages(True).clear_message_log()
        s.when_button_pressed_long(io_kitchen, 4)
        s.sequence().input(io_kitchen,4,True).afterAtLeast(1.5).input(io_kitchen,4,False).verify(timeout=6.0)
    """

    def __init__(self, scenario: "Scenario"):
        self.scenario = scenario
        self._steps: List[SequenceStep] = []
        self._pending_constraint: Optional[SequenceConstraint] = None

    # ---- fluent API: append typed steps ----
    def _append_step(self, kind: str, device: TestDevice, index: int, value: bool) -> SequenceBuilder:
        step = SequenceStep(
            kind=kind,
            device=device,
            index=int(index),
            value=1 if value else 0,
            constraint=self._pending_constraint,
        )
        self._pending_constraint = None
        self._steps.append(step)
        return self

    def input(self, device: "TestDevice", index: int, value: bool) -> SequenceBuilder:
        return self._append_step("input", device, index, value)

    def output(self, device: "TestDevice", index: int, value: bool) -> SequenceBuilder:
        return self._append_step("output", device, index, value)

    # ---- timing constraint setters (applied to NEXT appended step) ----
    def after(self, seconds: float) -> SequenceBuilder:
        self._pending_constraint = SequenceConstraint(type="min", min=float(seconds))
        return self

    def between(self, min_s: float, max_s: float) -> SequenceBuilder:
        self._pending_constraint = SequenceConstraint(type="between", min=float(min_s), max=float(max_s))
        return self

    def never(self) -> "SequenceBuilder":
        if not self._steps:
            raise ValueError("never() cannot be used as the first builder call")
        last = self._steps[-1]
        if last.kind not in ("input", "output"):
            raise ValueError("never() must follow an input() or output() step")
        # forbidden is opposite of last.value
        forbidden = 0 if last.value == 1 else 1
        # set pending constraint (will be applied to next appended step)
        self._pending_constraint = SequenceConstraint(type="never", forbidden_value=forbidden)
        return self

    def before(self, max_s: float) -> SequenceBuilder:
        self._pending_constraint = SequenceConstraint(type="max", max=float(max_s))
        return self

    # ---- verification (typed, uses DeviceState.timestamp) ----
    def verify(self, timeout: float = 8.0) -> SequenceBuilder:
        sc = self.scenario
        if not getattr(sc, "_log_messages", False):
            raise RuntimeError("Historical logging not enabled. Call s.log_state_messages(True) before using sequence().")

        deadline = time.time() + timeout

        # make sure we collected currently available messages
        sc._route_incoming_messages_until(deadline)

        # helper: return DeviceState list for device name
        def _events_for(devname: str) -> List["DeviceState"]:
            return sc._device_messages.get(devname, [])

        prev_ts: Optional[float] = None  # epoch seconds

        for step_idx, step in enumerate(self._steps):
            devname = step.device.name
            bit_index = step.index
            expected = step.value
            constraint = step.constraint

            matched = False
            checked = 0
            never_forbidden_value = None
            never_window_start = None
            if constraint is not None and constraint.type == "never":
                never_forbidden_value = int(constraint.forbidden_value)
                never_window_start = prev_ts 

            # loop until deadline trying to match this step
            while time.time() < deadline and not matched:
                events = _events_for(devname)

                while checked < len(events):
                    device_state = events[checked]

                    checked += 1
                    # timestamp -> epoch seconds
                    ev_ts=device_state.timestamp.timestamp()

                    # ensure event occurs after previous match (if any)
                    if prev_ts is not None and ev_ts < prev_ts - MICROSECOND:
                        continue
                    
                    if never_forbidden_value is not None and ev_ts >= never_window_start - MICROSECOND:
                        bit_value = device_state.input_bit(bit_index) if step.kind == "input" else device_state.output_bit(bit_index)
                        if bit_value == never_forbidden_value:
                            raise AssertionError(
                                f"Forbidden value observed for {devname} bit {bit_index}: saw {never_forbidden_value} at {ev_ts} before matching expected step."
                            )

                    bit_value = device_state.input_bit(bit_index) if step.kind == "input" else device_state.output_bit(bit_index)

                    if bit_value != expected:
                        continue

                    # timing constraints relative to prev_ts
                    if constraint is not None and prev_ts is not None:
                        delta = ev_ts - prev_ts
                        if constraint.type == "min":
                            if delta + MICROSECOND < (constraint.min or 0.0):
                                # event too early, continue searching later events
                                continue
                        elif constraint.type == "max":
                            if delta - MICROSECOND > (constraint.max or float("inf")):
                                # this event is too late; later ones are even later -> continue scanning just in case earlier match exists
                                continue
                        elif constraint.type == "between":
                            mn = constraint.min or 0.0
                            mx = constraint.max or float("inf")
                            if not (mn - MICROSECOND <= delta <= mx + MICROSECOND):
                                continue

                    # success for this step
                    prev_ts = ev_ts
                    matched = True
                    break

                if matched:
                    break

                # not matched: attempt to gather more messages then retry
                sc._route_incoming_messages_until(deadline)

                time.sleep(0.01)

            if not matched and constraint.type != "never":
                # helpful debug
                recent = _events_for(devname)[-8:]
                raise AssertionError(
                    f"Sequence failed at step {step_idx} ({step}). Prev_ts={prev_ts}. "
                    f"Recent events for {devname} (last {len(recent)}): {recent}"
                )

        # all steps matched
        return self
