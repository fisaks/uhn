"""Chaos tests: TCP simulator disconnect and reconnect.

Verifies that the edge server detects TCP device failures and recovers
when the TCP simulator comes back online.
"""
import time


def _wait_for_device_status(mqtt_watcher, device_name, expected_status, timeout=15.0):
    """Wait until the edge publishes a state message for `device_name` with
    the expected status ("ok" or "error"). Returns the matching state dict
    or raises AssertionError on timeout."""
    from common.types import DeviceState

    deadline = time.time() + timeout
    last_state = None

    while time.time() < deadline:
        remaining = deadline - time.time()
        if remaining <= 0:
            break
        try:
            topic, payload = mqtt_watcher.wait_for(timeout=min(remaining, 1.0))
        except TimeoutError:
            continue

        # Parse state topic: uhn/<edge>/device/<name>/state
        parts = topic.split("/")
        if (len(parts) >= 5 and parts[2] == "device" and parts[4] == "state"
                and parts[3] == device_name):
            state = DeviceState.from_dict(payload)
            last_state = state
            if state.status == expected_status:
                return state

    raise AssertionError(
        f"Timeout waiting for {device_name} status=={expected_status}. "
        f"Last state: {last_state}"
    )


def test_tcp_disconnect_reconnect(sim_client, mqtt_watcher, tcp_io8_1):
    """Scenario:
    1. Verify edge reads TCP device ok (status = "ok")
    2. Stop TCP sim via admin endpoint
    3. Wait for edge to report "error" on TCP device
    4. Restart TCP sim
    5. Wait for edge to recover (status = "ok")
    """
    device_name = tcp_io8_1.name

    # Drain stale messages from previous tests
    mqtt_watcher.get_all_pending()

    # 1. Confirm device is currently healthy (waits for a fresh message)
    state = _wait_for_device_status(mqtt_watcher, device_name, "ok", timeout=15.0)
    assert state.status == "ok"

    # 2. Stop TCP simulator
    result = sim_client.admin_stop()
    assert result["status"] == "stopped"

    # 3. Wait for edge to report error
    state = _wait_for_device_status(mqtt_watcher, device_name, "error", timeout=15.0)
    assert state.status == "error"

    # 4. Restart TCP simulator
    result = sim_client.admin_start()
    assert result["status"] == "started"

    # 5. Wait for recovery
    state = _wait_for_device_status(mqtt_watcher, device_name, "ok", timeout=15.0)
    assert state.status == "ok"
