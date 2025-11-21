from dataclasses import dataclass
from datetime import datetime
import json
from typing import Literal, Optional, List, Dict, Any

from common.utils import bit_is_set, bytes_to_bitstring, decode_base64_bytes, parse_iso_truncate_to_micro


@dataclass
class RangeSpec:
    start: int
    count: int

    @staticmethod
    def from_dict(d: Dict[str, Any]) -> "RangeSpec":
        return RangeSpec(start=int(d["start"]), count=int(d["count"]))


@dataclass
class CatalogDevice:
    name: str
    unitId: int
    type: str
    busId: str
    digitalOutputs: Optional[RangeSpec] = None
    digitalInputs: Optional[RangeSpec] = None

    @staticmethod
    def from_dict(d: Dict[str, Any]) -> "CatalogDevice":
        return CatalogDevice(
            name=d["name"],
            unitId=int(d["unitId"]),
            type=d["type"],
            busId=d.get("busId", ""),  # or raise if required
            digitalOutputs=RangeSpec.from_dict(d["digitalOutputs"]) if d.get("digitalOutputs") else None,
            digitalInputs=RangeSpec.from_dict(d["digitalInputs"]) if d.get("digitalInputs") else None,
        )


@dataclass
class Catalog:
    devices: List[CatalogDevice]

    @staticmethod
    def from_dict(d: Dict[str, Any]) -> "Catalog":
        return Catalog(devices=[CatalogDevice.from_dict(x) for x in d.get("devices", [])])

@dataclass
class DeviceState:
    timestamp: datetime
    name: str
    status: Literal["ok", "error", "partial_error"]
    digitalOutputs: Optional[bytes] = None
    digitalInputs: Optional[bytes] = None
    analogOutputs: Optional[bytes] = None
    analogInputs: Optional[bytes] = None
    
    errors: Optional[List[str]] = None
    
    @staticmethod
    def from_dict(d: Dict[str, Any]) -> "DeviceState":
        ts_raw = d.get("timestamp",None)
        name = d.get("name",None)
        status = d.get("status",None)
        if ts_raw is None or name is None or status is None:
            raise ValueError("Missing required fields in DeviceState dict")
        
        ts = parse_iso_truncate_to_micro(ts_raw)

        return DeviceState(
            timestamp=ts,
            name=name,
            status=status,
            digitalOutputs=decode_base64_bytes(d.get("digitalOutputs", None)) if d.get("digitalOutputs") else None,
            digitalInputs=decode_base64_bytes(d.get("digitalInputs", None)) if d.get("digitalInputs") else None,
            analogOutputs=decode_base64_bytes(d.get("analogOutputs", None)) if d.get("analogOutputs") else None,
            analogInputs=decode_base64_bytes(d.get("analogInputs", None)) if d.get("analogInputs") else None,
            errors=d.get("errors", None)
        )
    def is_input_bit_set(self, index: int) -> bool:
        """Return True if input bit (index) is 1. Index 0 = LSB of first byte."""
        return bit_is_set(self.digitalInputs, index)

    def input_bit(self, index: int) -> int:
        """Return 1 or 0 for the given input bit index."""
        return 1 if self.is_input_bit_set(index) else 0

    def is_output_bit_set(self, index: int) -> bool:
        """Return True if output bit (index) is 1."""
        return bit_is_set(self.digitalOutputs, index)

    def output_bit(self, index: int) -> int:
        """Return 1 or 0 for the given output bit index."""
        return 1 if self.is_output_bit_set(index) else 0
    
    def output_bits(self) -> list[int]:
        """
        Return coil bits in human order:
        MSB of highest byte first → LSB of lowest byte last.

        Example for 16 coils:
        - If bit 15 is 1, result starts with [1, 0, 0, ...].
        """
        if self.digitalOutputs is None:
            return []

        bits = []

        # Process bytes from highest to lowest
        for byte in reversed(self.digitalOutputs):
            # Extract bits MSB → LSB (bit 7 first, bit 0 last)
            for bit in range(7, -1, -1):
                bits.append((byte >> bit) & 1)

        return bits

    def input_bits(self) -> list[int]:
        if self.digitalInputs is None:
            return []

        bits = []

        # Process bytes from highest to lowest
        for byte in reversed(self.digitalInputs):
            # Extract bits MSB → LSB (bit 7 first, bit 0 last)
            for bit in range(7, -1, -1):
                bits.append((byte >> bit) & 1)

        return bits

    def to_log_dict(self) -> Dict[str, Any]:
        """Return a dict where digital IO is formatted as bit strings for logging."""
        return {
            "timestamp": self.timestamp.isoformat() if self.timestamp else None,
            "name": self.name,
            "digitalOutputs": bytes_to_bitstring(self.digitalOutputs) if self.digitalOutputs is not None else None,
            "digitalInputs": bytes_to_bitstring(self.digitalInputs) if self.digitalInputs is not None else None,
            "analogOutputs": self.analogOutputs,
            "analogInputs": self.analogInputs,
            "status": self.status,
            "errors": self.errors,
        }

    def __repr__(self) -> str:
        return f"{self.to_log_dict()}"

@dataclass
class DeviceCommand:
    device: str
    action: Literal["setdigitaloutput"]
    address: int
    value: int
    id: Optional[str] = None
    pulseMs: Optional[int] = None


    def to_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {
            "device": self.device,
            "action": self.action,
            "address": self.address,
            "value": self.value,
        }
        if self.id is not None:
            d["id"] = self.id
        if self.pulseMs is not None:
            d["pulseMs"] = self.pulseMs
        return d
    
    def to_json(self) -> str:
        return json.dumps(self.to_dict())
    
@dataclass
class Command:
    action: Literal["resync"]
    id: Optional[str]= None

    def to_dict(self) -> Dict[str, Any]:
        d: Dict[str, Any] = {
            "action": self.action,
        }
        if self.id is not None:
            d["id"] = self.id
        return d
    
    def to_json(self) -> str:
        return json.dumps(self.to_dict())   