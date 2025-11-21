import base64
from datetime import datetime 
import re
from typing import Optional


def decode_base64_bytes(s: str) -> bytes:
    return base64.b64decode(s)


def bit_is_set(byte_arr: bytes, bit_index: int) -> bool:
    byte_idx = bit_index // 8
    bit = bit_index % 8
    if byte_idx >= len(byte_arr):
        return False
    return (byte_arr[byte_idx] & (1 << bit)) != 0

def bytes_to_bitstring(bytes: bytes) -> str:
        if not bytes:
            return ""  # empty
        # format each byte as 8-bit binary (MSB left, LSB right)
        return " ".join(f"{byte:08b}" for byte in reversed(bytes))

#2025-11-21T21:21:02.043948229+02:00
_ISO_TRUNC_RE = re.compile(
    r'^(?P<head>\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})'
    r'(?:\.(?P<frac>\d+))?'
    r'(?P<tz>Z|[+-]\d{2}:\d{2})?$'
)

def parse_iso_truncate_to_micro(s: str) -> datetime:
    m = _ISO_TRUNC_RE.match(s)
    if not m:
        # fallback - let fromisoformat raise the error
        return datetime.fromisoformat(s.replace("Z", "+00:00"))

    head = m.group("head")
    frac = m.group("frac") or ""
    tz = m.group("tz") or ""

    if frac:
        # keep at most 6 digits (microseconds), pad right if shorter
        micros = (frac + "000000")[:6]
        iso = f"{head}.{micros}{tz}"
    else:
        iso = f"{head}{tz}"

    iso = iso.replace("Z", "+00:00")
    return datetime.fromisoformat(iso)

def _parse_iso_ts(s: Optional[str]) -> Optional[datetime]:
    if not s:
        return None
    try:
        # handle trailing Z for UTC
        if s.endswith("Z"):
            s = s[:-1] + "+00:00"
        return datetime.fromisoformat(s)
    except Exception:
        return None
