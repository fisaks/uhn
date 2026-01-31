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

Starts the development server in a tmux session with multiple windows for different services

`make dev`

Press `Ctrl-b + d` to detach the session, then run the following command to stop the development server:

`make dev-stop`

Can mouse copy when holding shift down

Uses go Air for go watch devserver.sh start mqtt in docker uhn runs locally

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

## Simulator

Rest interface

```sh
# Get whole device state

curl -s http://localhost:8080/device/{busId}/{device}

# Partial update (PATCH semantics)

curl -s -X PATCH http://localhost:8080/device/{busId}/{device} 
  -H 'Content-Type: application/json' 
  -d '{"digitalOutputs":[1,0,0,1],"analogOutputs":[123,456]}'

# Get/set a single digital output

curl -s http://localhost:8080/device/{busId}/{device}/digitalOutput/{i}
curl -s -X PUT http://localhost:8080/device/{busId}/{device}/digitalOutput/{i} 
  -H 'Content-Type: application/json' -d '{"value":1}'

# Toggle a digital output

curl -s -X POST http://localhost:8080/device/{busId}/{device}/digitalOutput/{i}/toggle

# Get/set/toggle/press a digital input

curl -s http://localhost:8080/device/{busId}/{device}/digitalInput/{i}
curl -s -X PUT http://localhost:8080/device/{busId}/{device}/digitalInput/{i} 
  -H 'Content-Type: application/json' -d '{"value":1}'
curl -s -X POST http://localhost:8080/device/{busId}/{device}/digitalInput/{i}/toggle
curl -s -X POST http://localhost:8080/device/{busId}/{device}/digitalInput/{i}/press/tap
curl -s -X POST http://localhost:8080/device/{busId}/{device}/digitalInput/{i}/press/hold1
curl -s -X POST http://localhost:8080/device/{busId}/{device}/digitalInput/{i}/press/hold2

# Analog registers

curl -s http://localhost:8080/device/{busId}/{device}/analogOutput/{i}
curl -s -X PUT http://localhost:8080/device/{busId}/{device}/analogOutput/{i}
  -H 'Content-Type: application/json' -d '{"value":65535}'
```

## Navigating the tmux Environment

- **Detach the tmux Session:** Press `Ctrl+b` and then `d` to detach the session while leaving the processes running.
- Can mouse copy when holding shift down
- **Switch Between Panes:** Use `Ctrl+b` followed by an `arrow key` to switch between panes.
- **Toggle Pane Size:** To make the current pane the whole window, press `Ctrl+b` and then `z`. To toggle back, press `Ctrl+b` and then `z` again.
- **Reattach to the tmux Session:** Run `tmux attach-session -t uhn-dev` or just `devserver.sh start`.
- **Kill the tmux Session:** Exit all panes or kill the session with `tmux kill-session -t uhn-dev` or just `devserver.sh stop`.

`tmux kill-session -t uhn-dev` or just `devserver.sh stop`

`tmux kill-server` Will kill the whole tmux server
