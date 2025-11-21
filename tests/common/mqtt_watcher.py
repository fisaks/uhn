import json
import queue
from threading import Event
import paho.mqtt.client as mqtt
from typing import Any, Tuple, Optional

from common.utils import bytes_to_bitstring, decode_base64_bytes


class MqttWatcher:
    def __init__(self, broker: str, port: int = 1883, client_id: Optional[str] = None):
        self.q: "queue.Queue[Tuple[str, Any]]" = queue.Queue()
        self.connected = Event()
        self._subscribed = Event()
        # self.client = mqtt.Client(client_id or f"test-{uuid.uuid4().hex}")
        self.client = mqtt.Client()
        self.client.on_connect = self._on_connect
        self.client.on_message = self._on_message
        self.client.on_subscribe = self._on_subscribe
        self.client.connect(broker, port)
        self.client.loop_start()

    def _on_connect(self, client, userdata, flags, rc):
        # rc == 0 -> success
        if rc == 0:
            self.connected.set()
        else:
            # optionally log rc/raise in tests
            pass

    def _on_subscribe(self, client, userdata, mid, granted_qos):
        # subscription acknowledged by broker
        self._subscribed.set()

    def _on_message(self, client, userdata, msg):
        payload = msg.payload.decode("utf-8", errors="replace")
        try:
            obj = json.loads(payload)
        except json.JSONDecodeError:
            obj = payload

         
            
        self._log_message(msg.topic, obj)
        self.q.put((msg.topic, obj))
    
    def _log_message(self, topic:str,obj:Any):
        display = dict(obj)
        for key in ("digitalOutputs", "digitalInputs"):
            if key in obj:
                raw = obj[key]
                if raw is None:
                    continue

                bytes = decode_base64_bytes(raw)
                if bytes is not None:
                    display[key] = bytes_to_bitstring(bytes)
                else:
                    display[key] = raw

        print(f"MQTT message received on topic {topic}: {display}",flush=True)

    def subscribe(self, topic: str, wait: float = 3.0) -> None:
        """Subscribe and wait for subscribe acknowledgement or connection."""
        # ensure connected first (wait up to `wait`)
        if not self.connected.wait(timeout=wait):
            raise RuntimeError("MQTT client did not connect in time")
        self._subscribed.clear()
        self.client.subscribe(topic)
        # wait for on_subscribe callback (broker ack)
        self._subscribed.wait(timeout=wait)

    def wait_for(self, timeout: float = 5.0) -> Tuple[str, Any]:
        try:
            return self.q.get(timeout=timeout)
        except queue.Empty:
            raise TimeoutError("Timeout waiting for MQTT message")

    def get_all_pending(self):
        items = []
        while True:
            try:
                items.append(self.q.get_nowait())
            except queue.Empty:
                break
        return items

    def publish(self, topic: str, payload: Any, qos: int = 0):
        data = json.dumps(payload) if isinstance(payload, dict) else str(payload)
        self.client.publish(topic, data, qos=qos)

    def stop(self):
        self.client.loop_stop()
        self.client.disconnect()

    # context manager support -- usage: "with MqttWatcher(...) as w:"
    def __enter__(self) -> "MqttWatcher":
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        try:
            self.stop()
        except Exception:
            pass
