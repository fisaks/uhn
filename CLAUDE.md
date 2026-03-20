# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

UHN (Unified Home Network) is a distributed home automation platform written in Go. It replaces legacy IHC installations using Modbus-based I/O devices. The system follows an edge-computing model: a Node.js **UHN Master** orchestrates the system, Go-based **UHN Edge** nodes handle device I/O and local logic, and an **MQTT broker** serves as the messaging backbone. User-defined rules execute in sandboxed Node.js runtimes.

## Build & Development Commands

```sh
# Install system deps + Go tools (air, delve)
make install-tools

# Start dev tmux session (mosquitto in Docker, edge locally with air live-reload)
# Profile-based: dev (default, all simulators), live (real hardware)
./devserver.sh start             # dev profile (default)
./devserver.sh live start        # live profile — real hardware
./devserver.sh live debug        # live profile with dlv debugger
./devserver.sh live stop         # stop live session

# Docker environments
make docker-dev          # Edge + RTU simulator + mosquitto
make docker-dev-real     # Edge with real serial devices
make docker-test         # Test environment
make docker-build        # Production images

# Build CLI tools to bin/
make build-uhn-tools

# Build edge server directly
go build -o bin/edge ./cmd/server/edge

# Run integration tests (requires docker-test environment running)
cd tests && python3 -m pytest
cd tests && python3 -m pytest edge/test_output.py -v
```

## Architecture

### Edge Server (`cmd/server/edge/main.go`)

The main entry point. On startup it: loads config from JSON, generates/loads an Ed25519 keypair for identity, connects to MQTT, publishes the device catalog and identity, creates per-bus pollers, subscribes to command topics, and runs the polling loop until shutdown.

### Key Packages (`internal/`)

- **config** — JSON config parser with comment stripping (`//`, `/**/`), strict validation with multi-error reporting, and default injection. `EdgeConfig` defines buses, devices, and polling settings.
- **messaging** — MQTT client wrapper (`Broker`) with QoS constants, JSON serialization, and topic prefixing (`uhn/{edgeName}/...`). `EdgeBroker` extends it with state change detection and heartbeat publishing.
- **poller** — One `SerialBusPoller` goroutine per Modbus bus. Polls all devices on a configurable interval, reads FC1-FC4 (digital/analog inputs/outputs) with chunked reads, queues incoming commands via buffered channels.
- **modbus** — Wraps `goburrow/modbus` for RTU and TCP. Handles connection backoff (200ms–5s), per-device slave IDs, chunked reads respecting Modbus limits (2000 bits / 125 registers).
- **state** — `EdgeStateStore` tracks last-published device state with byte-level change detection and heartbeat timestamps (thread-safe via RWMutex).
- **catalog** — Builds and publishes retained MQTT device inventory messages.
- **encrypt** — Ed25519 keypair generation/storage for edge authentication.
- **ihc** — IHC SOAP client (`soap_client.go`) and driver (`ihc_driver.go`). Handles authentication, resource subscription, long-poll notifications, and exponential backoff reconnection. Implements `DeviceDriver` interface with `BypassSignalState() = true`.
- **ihcsim** — IHC SOAP simulator for local development. Full SOAP protocol (auth, subscribe, long-poll notifications, setValue) + REST control plane (port 8090) for testing. Includes reactive bindings system (trigger input → toggle output). Entry point: `cmd/tools/ihc-sim/main.go`.
- **milight** — Mi-Light iBox2 driver. Transport (`milight_transport.go`) manages UDP v6 connection, per-bridge command queue with rate limiting (100ms between sends), assumed state publishing, and per-zone **gatekeeper** (checks an external physical resource — e.g. IHC mains relay — before sending; suppresses commands when OFF, resets night mode assumed state on OFF→ON transition). Driver (`milight_driver.go`) maps pins to FUT069 RGB+CCT commands. Implements `DeviceTransport` + `DeviceDriver` with `BypassSignalState() = true`.
- **milightsim** — Mi-Light UDP simulator for local development. Receives v6 packets (handshake, commands), sends ACK responses, tracks per-zone state (power, brightness, CCT, hue, saturation, mode). REST control plane on port 8091. Entry point: `cmd/tools/milight-sim/main.go`.
- **uhn** — Core domain types: `DeviceState`, `DeviceCommand`, and interfaces (`EdgePublisher`, `EdgeSubscriber`, `CommandPusher`, `DeviceTransport`, `DeviceDriver`). `DeviceTransport` manages connection lifecycle; `DeviceDriver` handles protocol-agnostic device interaction.

### Sandbox System (`cmd/tools/sandbox-*`, `docs/sandbox.md`)

Four-tier isolation for user rules: launcher → setup (runs as root, sets up namespaces/cgroup/chroot) → sandboxd (unprivileged supervisor) → Node.js runtime. Provides mount namespace, chroot, read-only filesystem, cgroup limits, and no network access.

### MQTT Topic Structure

- `uhn/{edgeName}/catalog` — Device inventory (retained)
- `uhn/{edgeName}/identity` — Edge public key
- `uhn/{edgeName}/device/{deviceName}/state` — Device state updates (Modbus byte arrays)
- `uhn/{edgeName}/device/{deviceName}/pin/{hexPin}` — Per-pin state (IHC)
- `uhn/{edgeName}/device/{deviceName}/cmd` — Device commands
- `uhn/{edgeName}/resource/state/{resourceId}` — Logical resource state (timers, virtual)
- `uhn/{edgeName}/resource/signal/{resourceId}` — Signal overrides from master
- `uhn/{edgeName}/resource/cmd/{resourceId}` — View commands (tap, longPress, setState)
- `uhn/{edgeName}/status` — Edge status

See `docs/mqtt-state-flow.md` for the full topic reference and state flow documentation.

### Development Profiles

`devserver.sh` supports profiles for switching between simulator and real hardware environments. Each profile uses two files:
- `config/edge-config-{profile}.json` — edge configuration
- `config/devserver-{profile}.conf` — simulator flags (`MODBUS_SIM`, `IHC_SIM`)

The tmux session (`uhn-{profile}`) has two windows:
- **dev** — MQTT monitor, Mosquitto logs, edge server, empty shell
- **sims** — Modbus simulator (left), IHC simulator (right)

Mixed configurations are supported (e.g., `MODBUS_SIM=true` + `IHC_SIM=false` for real IHC with simulated Modbus).

### Integration Tests (`tests/`)

Python pytest suite that talks to the RTU simulator via HTTP and watches MQTT state topics. Requires the docker-test environment. Fixtures defined in `conftest.py` with `RtuSimClient` and `MqttWatcher` helpers.

## Configuration

Configuration uses three tiers with clear priority: **env var > config file > default**.

### Edge Config File (`edge.` section)

The JSON config file has an optional `edge` section for identity, connectivity, and initial runtime settings:

```json
{
    "edge": {
        "name": "edge1",
        "mqtt": "tcp://localhost:2883",
        "logLevel": "info",
        "runtimeMode": "normal",
        "debugPort": 9251,
        "debugHost": "192.168.1.10",
        "logFormat": "json"
    },
    "buses": [...]
}
```

All `edge` fields are optional — env vars or defaults apply when omitted.

### Environment Variables

**Infrastructure paths (env-only, not in config file):**
- `UHN_EDGE_CONFIG_PATH` — Path to edge config JSON (default: `/etc/uhn/edge-config.json`)
- `UHN_WORKSPACE_PATH` — Blueprint workspace root; if empty, runtime is disabled
- `UHN_SANDBOX_PATH` — Sandbox binary location (default: `/usr/lib/uhn`)
- `UHN_NODE_PATH` — Node.js install path (default: `/opt/node`)
- `UHN_RUNTIME_PATH` — Path to the installed Node rule-runtime; required to start a local runtime
- `TZ` — Timezone for sandbox (default: `UTC`)

**Override env vars (take priority over config file):**
- `UHN_EDGE_NAME` — Overrides `edge.name` (required — no default)
- `UHN_MQTT_URL` — Overrides `edge.mqtt` (default: `tcp://localhost:1883`)
- `UHN_LOG_LEVEL` — Overrides `edge.logLevel` (default: `info`)
- `UHN_LOG_FORMAT` — Overrides `edge.logFormat` (default: `json`)
- `UHN_RUNTIME_MODE` — Overrides `edge.runtimeMode` (default: `normal`)
- `UHN_DEBUG_PORT` — Overrides `edge.debugPort` (default: auto from edge name hash)
- `UHN_PUBLIC_HOST` — Overrides `edge.debugHost` (display-only host/IP for debug port, no default)

### MQTT Runtime Overrides (highest priority)

`logLevel`, `runtimeMode`, and `debugPort` can be changed at runtime via system commands from UHN Master. These values are persisted to `$UHN_WORKSPACE_PATH/runtime-config.json` and take precedence over env vars and config file values, even across edge restarts. Note: `debugHost` is NOT overridden by MQTT — it is resolved only from env var or config file.
