import pytest
from dataclasses import dataclass
from common.sim_client import SimClient
from common.mqtt_watcher import MqttWatcher
from dsl.device_model import TestDevice

STATE_TOPIC = "uhn/+/device/+/state"
CATALOG_TOPIC="uhn/+/catalog"

#DEFAULT_EDGE_NAME = "edge-dev-1"
DEFAULT_EDGE_NAME = "edge1"

def pytest_addoption(parser):
    parser.addoption(
        "--edge-name",
        action="store",
        default=DEFAULT_EDGE_NAME,
        help="Edge name to use for TestDevice fixtures (default: edge-dev-1)"
    )

@pytest.fixture
def edge_name(request) -> str:
    return request.config.getoption("--edge-name")

@pytest.fixture(scope="session")
def sim_client():
    return SimClient("http://localhost:8080")

@pytest.fixture(scope="session")
def mqtt_watcher():
    w = MqttWatcher("localhost", 1883)
    w.subscribe(STATE_TOPIC)
    w.subscribe(CATALOG_TOPIC)
    yield w
    w.stop()

# --- RTU device fixtures ---

@pytest.fixture
def kitchen_io8_1(edge_name) -> TestDevice:
    return TestDevice(bus="bus_a", name="kitchen_io8_1", edge_name=edge_name)

@pytest.fixture
def kitchen_relay8_1(edge_name) -> TestDevice:
    return TestDevice(bus="bus_a", name="kitchen_relay8_1", edge_name=edge_name)
@pytest.fixture
def io_test16_out(edge_name) -> TestDevice:
    return TestDevice(bus="bus_a", name="io_test16_out", edge_name=edge_name)

@pytest.fixture
def di_test16_in(edge_name) -> TestDevice:
    return TestDevice(bus="bus_a", name="di_test16_in", edge_name=edge_name)

# --- TCP device fixtures ---

@pytest.fixture
def tcp_io8_1(edge_name) -> TestDevice:
    return TestDevice(bus="bus_tcp", name="tcp_io8_1", edge_name=edge_name)

@pytest.fixture
def tcp_relay8_1(edge_name) -> TestDevice:
    return TestDevice(bus="bus_tcp", name="tcp_relay8_1", edge_name=edge_name)

@pytest.fixture
def tcp_io16_out(edge_name) -> TestDevice:
    return TestDevice(bus="bus_tcp", name="tcp_io16_out", edge_name=edge_name)

@pytest.fixture
def tcp_di16_in(edge_name) -> TestDevice:
    return TestDevice(bus="bus_tcp", name="tcp_di16_in", edge_name=edge_name)

@pytest.fixture
def energy_meter(edge_name) -> TestDevice:
    return TestDevice(bus="bus_tcp", name="energy_meter_1", edge_name=edge_name)

# --- Protocol-parametrized environment ---

@dataclass
class ProtocolEnv:
    """Bundles sim client + device fixtures for a specific protocol."""
    protocol: str
    sim: SimClient
    io8_1: TestDevice
    relay8_1: TestDevice
    io16_out: TestDevice
    di16_in: TestDevice

@pytest.fixture(params=["rtu", "tcp"])
def env(request, edge_name, sim_client) -> ProtocolEnv:
    if request.param == "rtu":
        return ProtocolEnv(
            protocol="rtu",
            sim=sim_client,
            io8_1=TestDevice(bus="bus_a", name="kitchen_io8_1", edge_name=edge_name),
            relay8_1=TestDevice(bus="bus_a", name="kitchen_relay8_1", edge_name=edge_name),
            io16_out=TestDevice(bus="bus_a", name="io_test16_out", edge_name=edge_name),
            di16_in=TestDevice(bus="bus_a", name="di_test16_in", edge_name=edge_name),
        )
    else:
        return ProtocolEnv(
            protocol="tcp",
            sim=sim_client,
            io8_1=TestDevice(bus="bus_tcp", name="tcp_io8_1", edge_name=edge_name),
            relay8_1=TestDevice(bus="bus_tcp", name="tcp_relay8_1", edge_name=edge_name),
            io16_out=TestDevice(bus="bus_tcp", name="tcp_io16_out", edge_name=edge_name),
            di16_in=TestDevice(bus="bus_tcp", name="tcp_di16_in", edge_name=edge_name),
        )
