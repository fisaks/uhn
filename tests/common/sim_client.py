# file: tests/common/sim_client.py
from typing import Any, Dict, Literal, Optional
import requests
import time


class SimClientError(Exception):
    pass


class SimClient:
    """
    Lightweight client wrapper for the Modbus simulator REST API.

    Usage:
        sim = SimClient("http://localhost:8080", timeout=3.0)
        sim.set_digital_output("bus1", "io_kitchen", 3, 1)
        sim.wait_for_digital_output("bus1", "io_kitchen", 3, expected=1, timeout=5.0)
    """

    def __init__(
        self,
        base_url: str,
        timeout: float = 3.0,
        session: Optional[requests.Session] = None,
    ):
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        self.session = session or requests.Session()

    # --- low-level helpers ---
    def _url(self, path: str) -> str:
        return f"{self.base_url}{path}"

    def _request(self, method: str, path: str, **kwargs) -> requests.Response:
        url = self._url(path)
        kwargs.setdefault("timeout", self.timeout)
        try:
            resp = self.session.request(method, url, **kwargs)
        except requests.RequestException as e:
            raise SimClientError(f"request error {method} {url}: {e}") from e
        if not (200 <= resp.status_code < 300):
            raise SimClientError(f"{method} {url} -> {resp.status_code}: {resp.text}")
        return resp

    # --- API methods ---
    def get_device_state(self, bus_id: str, device_name: str) -> Dict[str, Any]:
        path = f"/device/{bus_id}/{device_name}"
        resp = self._request("GET", path)
        return resp.json()

    def patch_device(
        self, bus_id: str, device_name: str, patch: Dict[str, Any]
    ) -> Dict[str, Any]:
        path = f"/device/{bus_id}/{device_name}"
        resp = self._request("PATCH", path, json=patch)
        return resp.json()

    # Digital outputs
    def get_digital_output(
        self, bus_id: str, device_name: str, index: int
    ) -> Dict[str, Any]:
        path = f"/device/{bus_id}/{device_name}/digitalOutput/{index}"
        resp = self._request("GET", path)
        return resp.json()

    def set_digital_output(
        self, bus_id: str, device_name: str, index: int, value: int
    ) -> Dict[str, Any]:
        path = f"/device/{bus_id}/{device_name}/digitalOutput/{index}"
        resp = self._request("PUT", path, json={"value": int(value)})
        return resp.json()

    def toggle_digital_output(
        self, bus_id: str, device_name: str, index: int
    ) -> Dict[str, Any]:
        path = f"/device/{bus_id}/{device_name}/digitalOutput/{index}/toggle"
        resp = self._request("POST", path, json={})
        return resp.json()

    # Digital inputs
    def get_digital_input(
        self, bus_id: str, device_name: str, index: int
    ) -> Dict[str, Any]:
        path = f"/device/{bus_id}/{device_name}/digitalInput/{index}"
        resp = self._request("GET", path)
        return resp.json()

    def set_digital_input(
        self, bus_id: str, device_name: str, index: int, value: int
    ) -> Dict[str, Any]:
        path = f"/device/{bus_id}/{device_name}/digitalInput/{index}"
        resp = self._request("PUT", path, json={"value": int(value)})
        return resp.json()

    def toggle_digital_input(
        self, bus_id: str, device_name: str, index: int
    ) -> Dict[str, Any]:
        path = f"/device/{bus_id}/{device_name}/digitalInput/{index}/toggle"
        resp = self._request("POST", path, json={})
        return resp.json()

    # Analog access
    def get_analog_output(
        self, bus_id: str, device_name: str, index: int
    ) -> Dict[str, Any]:
        path = f"/device/{bus_id}/{device_name}/analogOutput/{index}"
        resp = self._request("GET", path)
        return resp.json()

    def set_analog_output(
        self, bus_id: str, device_name: str, index: int, value: int
    ) -> Dict[str, Any]:
        path = f"/device/{bus_id}/{device_name}/analogOutput/{index}"
        resp = self._request("PUT", path, json={"value": int(value)})
        return resp.json()

    def get_analog_input(
        self, bus_id: str, device_name: str, index: int
    ) -> Dict[str, Any]:
        path = f"/device/{bus_id}/{device_name}/analogInput/{index}"
        resp = self._request("GET", path)
        return resp.json()

    def set_analog_input(
        self, bus_id: str, device_name: str, index: int, value: int
    ) -> Dict[str, Any]:
        path = f"/device/{bus_id}/{device_name}/analogInput/{index}"
        resp = self._request("PUT", path, json={"value": int(value)})
        return resp.json()

    # Toggles / press
    def press_digital_input(
        self, bus_id: str, device_name: str, index: int, mode: Literal["tap","hold1","hold2"]
    ) -> Dict[str, Any]:
        # mode could be "short", "long", "hold" — depends on sim implementation
        path = f"/device/{bus_id}/{device_name}/digitalInput/{index}/press/{mode}"
        resp = self._request("POST", path, json={})
        return resp.json()

    # Healthcheck (optional)
    def health(self) -> bool:
        try:
            resp = self._request("GET", "/")
            return True
        except SimClientError:
            return False

    # Admin lifecycle control (for chaos testing)
    def admin_stop(self) -> Dict[str, Any]:
        resp = self._request("POST", "/admin/stop")
        return resp.json()

    def admin_start(self) -> Dict[str, Any]:
        resp = self._request("POST", "/admin/start")
        return resp.json()

    # Profile ticker control
    def ticker_enable(self) -> Dict[str, Any]:
        resp = self._request("POST", "/admin/ticker/enable")
        return resp.json()

    def ticker_disable(self) -> Dict[str, Any]:
        resp = self._request("POST", "/admin/ticker/disable")
        return resp.json()

    def ticker_status(self) -> Dict[str, Any]:
        resp = self._request("GET", "/admin/ticker/status")
        return resp.json()

    # Convenience wait helper: poll until the digital output matches expected or timeout
    def wait_for_digital_output(
        self,
        bus_id: str,
        device_name: str,
        index: int,
        expected: int,
        timeout: float = 5.0,
        poll_interval: float = 0.1,
    ) -> bool:
        deadline = time.time() + timeout
        while time.time() < deadline:
            try:
                res = self.get_digital_output(bus_id, device_name, index)
            except SimClientError:
                time.sleep(poll_interval)
                continue
            val = int(res.get("value", 0))
            if val == expected:
                return True
            time.sleep(poll_interval)
        return False
