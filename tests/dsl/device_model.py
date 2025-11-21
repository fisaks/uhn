from dataclasses import dataclass

@dataclass
class TestDevice:
    bus: str
    name: str
    edge_name: str = "edge1"
