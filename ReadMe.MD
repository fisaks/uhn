# Unified Home Network

> 🚧 **WORK IN PROGRESS** 🚧

A personal project to create an open, distributed automation platform—
one day replacing my aging IHC installation with something I control, extend, and trust.

My aim is to build a flexible replacement for my legacy IHC setup—capable of supporting both wired (e.g. Modbus, GPIO, I²C) and wireless (e.g. Zigbee) devices, with no artificial hardware limits.

**Not limited by vendor, protocol, or device count.**

The goal is something like this

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                                BROWSER                                       │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │                    UHN UI (React app)                                  │  │
│  │                                                                        │  │
│  │  - Runs in browser                                                     │  │
│  │  - Loaded through UXP                                                  │  │
│  │  - Talks to UHN Master via REST + WebSocket                            │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
└───────────────────────────────▲──────────────────────────────────────────────┘
                                │ REST / WebSocket
                                │
┌───────────────────────────────▼──────────────────────────────────────────────┐
│                              UXP                                             │
│  - REST + WebSocket Reverse Proxy                                            │
│  - UXP Common Libs                                                           │ 
│  - Authentication and Authorization                                          │
│                                                                              │
└───────────────────────────────▲──────────────────────────────────────────────┘
                                │
                                │
┌───────────────────────────────▼┴─────────────────────────────────────────────┐
│                               UHN MASTER                                     │
│                        (Node.js / UXP remote app)                            │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │ REST API                                                               │  │
│  │ WebSocket API                                                          │  │
│  │                                                                        │  │
│  │ - Blueprint upload / management                                        │  │
│  │ - Global coordination                                                  │  │
│  │ - Rule placement (master vs edge)                                      │  │
│  │ - State aggregation from edges                                         │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │ MQTT Broker (Master-local)                                             │  │
│  │                                                                        │  │
│  │ - Receives state/events from edges                                     │  │
│  │ - Sends commands / assignments to edges                                │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │ Go Sandbox Launcher (local)                                            │  │
│  │                                                                        │  │
│  │  ┌──────────────────────────────────────────────────────────────────┐  │  │
│  │  │ RuleRuntime (Node.js, sandboxed)                                 │  │  │
│  │  │                                                                  │  │  │
│  │  │ - Executes rules assigned to master                              │  │  │
│  │  │ - Read-only state mirror                                         │  │  │
│  │  │ - Timers owned locally (for master rules)                        │  │  │
│  │  │ - stdin/stdout to UHN Master Node                                │  │  │
│  │  │ - No MQTT, no REST, no WS, no devices                            │  │  │
│  │  │ - Deterministic                                                  │  │  │
│  │  │ - No I/O                                                         │  │  │
│  │  │ - No network                                                     │  │  │
│  │  │ - Pure logic + timers                                            │  │  │
│  │  └──────────────────────────────────────────────────────────────────┘  │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
│                                                                              │
└───────────────────────────────┬──────────────────────────────────────────────┘
                                │
                                │ MQTT bridge (bi-directional)
                                │
        ┌───────────────────────┴───────────────────────┐
        │                                               │
┌───────▼──────────────────┐               ┌────────────▼──────────────────┐
│        UHN EDGE #1       │               │        UHN EDGE #N            │
│           (Go)           │               │           (Go)                │
│                          │               │                               │
│  ┌─────────────────────┐ │               │ ┌────────────────────────────┐│
│  │ MQTT Broker (local) │ │               │ │ MQTT Broker (local)        ││
│  │ (bridged to master) │ │               │ │ (bridged to master)        ││
│  └─────────────────────┘ │               │ └────────────────────────────┘│
│                          │               │                               │
│  - Device I/O            │               │ - Device I/O                  │
│  - Local state           │               │ - Local state                 │
│  - Local timers          │               │ - Local timers                │
│                          │               │                               │
│  ┌─────────────────────┐ │               │ ┌────────────────────────────┐│
│  │ Go Sandbox Launcher │ │               │ │ Go Sandbox Launcher        ││
│  │                     │ │               │ │                            ││
│  │ ┌─────────────────┐ │ │               │ │ ┌─────────────────────────┐││
│  │ │ RuleRuntime     │ │ │               │ │ │ RuleRuntime             │││
│  │ │ (Node.js)       │ │ │               │ │ │ (Node.js)               │││
│  │ │ sandboxed       │ │ │               │ │ │ sandboxed               │││
│  │ │                 │ │ │               │ │ │                         │││
│  │ │ - Executes      │ │ │               │ │ │ - Executes              │││
│  │ │   edge rules    │ │ │               │ │ │   edge rules            │││
│  │ │ - Read-only     │ │ │               │ │ │ - Read-only             │││
│  │ │   state mirror  │ │ │               │ │ │   state mirror          │││
│  │ │ - Timers local  │ │ │               │ │ │ - Timers local          │││
│  │ │ - stdin/stdout  │ │ │               │ │ │ - stdin/stdout          │││
│  │ │ - no MQTT       │ │ │               │ │ │ - no MQTT               │││
│  │ │ - no REST/WS    │ │ │               │ │ │ - no REST/WS            │││
│  │ │ - no devices    │ │ │               │ │ │ - no devices            │││
│  │ │ - Can escalate  │ │ │               │ │ │ - Can escalate          │││
│  │ │   rule to run   │ │ │               │ │ │   rule to run           │││
│  │ │   on master     │ │ │               │ │ │   on master             │││
│  │ └─────────────────┘ │ │               │ │ └─────────────────────────┘││
│  └─────────────────────┘ │               │ └────────────────────────────┘│
└──────────────────────────┘               └───────────────────────────────┘
```

## More Documentation

https://github.com/fisaks/uhn/blob/main/docs

## Dev setup

Install tools:

```sh
go install github.com/air-verse/air@$(go list -m -f '{{.Version}}' github.com/air-verse/air)
```

or just `make install-tools`

## Development environment

Starts the development server in a tmux session with two windows:
- **dev** — MQTT monitor, Mosquitto logs, edge server, empty shell
- **sims** — Modbus simulator (left), IHC simulator (right)

### Profiles

```sh
./devserver.sh start             # dev profile (default) — all simulators
./devserver.sh live start        # live profile — real hardware
./devserver.sh live debug        # live profile with dlv debugger
./devserver.sh live stop         # stop live session
```

Each profile uses two files:
- `config/edge-config-{profile}.json` — edge configuration
- `config/devserver-{profile}.conf` — simulator flags (`MODBUS_SIM`, `IHC_SIM`)

The `dev` profile runs all simulators. The `live` profile can be customized per-flag, e.g. `MODBUS_SIM=true` + `IHC_SIM=false` for real IHC with simulated Modbus.

Press `Ctrl-b + d` to detach the session. Switch windows with `Ctrl-b + n` / `Ctrl-b + p`.

Uses go Air for hot reload. Mosquitto runs in Docker, edge runs locally.

## Docker

To run dev in docker using rtu simulation software `docker compose --profile dev up --build` or `make docker-dev`

To run dev-real in docker using real rtu devices `docker compose --profile dev-real up --build` or `make docker-dev`-real

To build prod images `docker compose --profile prod build` or `make docker-build`

## MQTT

To tail messages

`docker exec -it uhn-mosquitto sh -c 'apk add --no-cache mosquitto-clients >/dev/null 2>&1 || true; mosquitto_sub -h localhost -t "uhn/#" -v'`

## Tools

To see the base64 binary data as bits `echo AQ== | base64 -d | od -An -t u1`

make build-uhn-tools

./bin/uhn-monitor

./bin/uhnctl push --edge edge1 --device io_kitchen --address 7 --value 1

## Simulators

### Modbus Simulator (port 8080)

REST interface for controlling simulated Modbus devices.

```sh
# Get whole device state
curl -s http://localhost:8080/device/{busId}/{device}

# Partial update (PATCH semantics)
curl -s -X PATCH http://localhost:8080/device/{busId}/{device} \
  -H 'Content-Type: application/json' \
  -d '{"digitalOutputs":[1,0,0,1],"analogOutputs":[123,456]}'

# Get/set a single digital output
curl -s http://localhost:8080/device/{busId}/{device}/digitalOutput/{i}
curl -s -X PUT http://localhost:8080/device/{busId}/{device}/digitalOutput/{i} \
  -H 'Content-Type: application/json' -d '{"value":1}'

# Toggle a digital output
curl -s -X POST http://localhost:8080/device/{busId}/{device}/digitalOutput/{i}/toggle

# Get/set/toggle/press a digital input
curl -s http://localhost:8080/device/{busId}/{device}/digitalInput/{i}
curl -s -X PUT http://localhost:8080/device/{busId}/{device}/digitalInput/{i} \
  -H 'Content-Type: application/json' -d '{"value":1}'
curl -s -X POST http://localhost:8080/device/{busId}/{device}/digitalInput/{i}/toggle
curl -s -X POST http://localhost:8080/device/{busId}/{device}/digitalInput/{i}/press/tap
curl -s -X POST http://localhost:8080/device/{busId}/{device}/digitalInput/{i}/press/hold1
curl -s -X POST http://localhost:8080/device/{busId}/{device}/digitalInput/{i}/press/hold2

# Analog registers
curl -s http://localhost:8080/device/{busId}/{device}/analogOutput/{i}
curl -s -X PUT http://localhost:8080/device/{busId}/{device}/analogOutput/{i} \
  -H 'Content-Type: application/json' -d '{"value":65535}'
```

#### Analog register types

Analog GET/PUT endpoints support typed reads and writes for multi-register values.

| Type | Registers | Range |
|------|-----------|-------|
| `uint16` (default) | 1 | 0 .. 65535 |
| `int16` | 1 | -32768 .. 32767 |
| `float32` | 2 | IEEE 754 single-precision |
| `uint32` | 2 | 0 .. 4294967295 |
| `int32` | 2 | -2147483648 .. 2147483647 |

All multi-register values use big-endian byte order (high word first).

**Reading** — pass `?type=` query parameter:

```sh
curl -s 'http://localhost:8080/device/{busId}/{device}/analogInput/{i}?type=float32'
```

**Writing** — include an optional `"type"` field in the JSON body. When omitted, a decimal value is auto-detected as `float32`, an integer as `uint16`.

```sh
curl -s -X PUT .../analogInput/{i} -d '{"value": 72.2}'
curl -s -X PUT .../analogInput/{i} -d '{"value": -5, "type": "int16"}'
```

#### Profile ticker

Device profiles simulate periodic register changes (e.g. Shelly Pro 3EM energy readings). The ticker starts **disabled** and can be controlled via REST:

```sh
curl -s -X POST http://localhost:8080/admin/ticker/enable
curl -s http://localhost:8080/admin/ticker/status
curl -s -X POST http://localhost:8080/admin/ticker/disable
```

### IHC Simulator (SOAP on port 8180, REST on port 8090)

Simulates an IHC controller with full SOAP protocol support. The edge connects to it like a real IHC controller — authentication, resource subscription, long-poll notifications, and setValue all work.

Resources are initialized from `ihcControllers[].resources` in the edge config. Credentials come from `config/ihc-credentials-dev.json`.

#### REST control plane

```sh
# List all resources on a controller
curl -s http://localhost:8090/controller/ihc2/resources

# Get/set a single resource (hex or decimal ID)
curl -s http://localhost:8090/controller/ihc2/resource/0x9F085E
curl -s -X PUT http://localhost:8090/controller/ihc2/resource/0x9F085E \
  -H 'Content-Type: application/json' -d '{"value": true}'

# Toggle a boolean resource
curl -s -X POST http://localhost:8090/controller/ihc2/resource/0x9F045C/toggle

# Simulate button press (tap=50ms, hold1=500ms, hold2=1500ms)
curl -s -X POST http://localhost:8090/controller/ihc2/resource/0x9F045C/press/tap

# Session management
curl -s http://localhost:8090/controller/ihc2/sessions
curl -s -X POST http://localhost:8090/controller/ihc2/session/expire

# Admin: stop/start SOAP server (outage simulation)
curl -s http://localhost:8090/admin/status
curl -s -X POST http://localhost:8090/admin/stop/ihc2
curl -s -X POST http://localhost:8090/admin/start/ihc2
```

#### Bindings

Bindings create simple reactive links between resources — e.g. tapping an input toggles an output. Loaded from `config/ihc-sim-bindings.json` at startup and manageable via REST at runtime.

Config file format:

```json
{
    "bindings": [
        {
            "controller": "ihc2",
            "trigger": "0x9F045C",
            "action": "toggle",
            "target": "0x9F085E"
        }
    ]
}
```

Supported actions: `toggle` (fires on rising edge of trigger).

```sh
# List all bindings
curl -s http://localhost:8090/bindings

# Add a binding at runtime
curl -s -X POST http://localhost:8090/bindings \
  -H 'Content-Type: application/json' \
  -d '{"controller":"ihc2","trigger":"0x9F045C","action":"toggle","target":"0x9F085E"}'

# Remove a binding
curl -s -X DELETE http://localhost:8090/bindings/b1
```

## Navigating the tmux Environment

Session name is `uhn-{profile}` (e.g. `uhn-dev`, `uhn-live`).

- **Detach:** `Ctrl+b d` — leaves processes running
- **Switch windows:** `Ctrl+b n` (next) / `Ctrl+b p` (previous) — toggle between dev and sims windows
- **Switch panes:** `Ctrl+b arrow` — navigate between panes in current window
- **Zoom pane:** `Ctrl+b z` — toggle current pane fullscreen
- **Mouse copy:** hold `Shift` while selecting
- **Reattach:** `tmux attach-session -t uhn-dev` or just `./devserver.sh start`
- **Stop:** `./devserver.sh stop` or `./devserver.sh live stop`
- **Kill all tmux:** `tmux kill-server`
