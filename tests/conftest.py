import pytest
from common.rtu_sim_client import RtuSimClient
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
def rtu_sim_client():
    return RtuSimClient("http://localhost:8080")

@pytest.fixture(scope="session")
def mqtt_watcher():
    w = MqttWatcher("localhost", 1883)
    w.subscribe(STATE_TOPIC)
    w.subscribe(CATALOG_TOPIC)
    yield w
    w.stop()

@pytest.fixture
def io_kitchen(edge_name) -> TestDevice:
    return TestDevice(bus="bus_a", name="io_kitchen", edge_name=edge_name)

@pytest.fixture
def relay_heating(edge_name) -> TestDevice:
    return TestDevice(bus="bus_a", name="relay_heating", edge_name=edge_name)

@pytest.fixture
def io_test16_out(edge_name) -> TestDevice:
    return TestDevice(bus="bus_a", name="io_test16_out", edge_name=edge_name)

@pytest.fixture
def di_test16_in(edge_name) -> TestDevice:
    return TestDevice(bus="bus_a", name="di_test16_in", edge_name=edge_name)    