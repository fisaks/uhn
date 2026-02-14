# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

UHN (Unified Home Network) is a distributed home automation platform written in Go. It replaces legacy IHC installations using Modbus-based I/O devices. The system follows an edge-computing model: a Node.js **UHN Master** orchestrates the system, Go-based **UHN Edge** nodes handle device I/O and local logic, and an **MQTT broker** serves as the messaging backbone. User-defined rules execute in sandboxed Node.js runtimes.

## Build & Development Commands

```sh
# Install system deps + Go tools (air, delve)
make install-tools

# Start dev tmux session (mosquitto in Docker, edge locally with air live-reload)
make dev
make dev-stop

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
- **uhn** — Core domain types: `DeviceState`, `DeviceCommand`, and interfaces (`EdgePublisher`, `EdgeSubscriber`, `CommandPusher`).

### Sandbox System (`cmd/tools/sandbox-*`, `docs/sandbox.md`)

Four-tier isolation for user rules: launcher → setup (runs as root, sets up namespaces/cgroup/chroot) → sandboxd (unprivileged supervisor) → Node.js runtime. Provides mount namespace, chroot, read-only filesystem, cgroup limits, and no network access.

### MQTT Topic Structure

- `uhn/{edgeName}/catalog` — Device inventory (retained)
- `uhn/{edgeName}/identity` — Edge public key
- `uhn/{edgeName}/device/{deviceName}/state` — Device state updates
- `uhn/{edgeName}/device/{deviceName}/cmd` — Device commands
- `uhn/{edgeName}/status` — Edge status

### Integration Tests (`tests/`)

Python pytest suite that talks to the RTU simulator via HTTP and watches MQTT state topics. Requires the docker-test environment. Fixtures defined in `conftest.py` with `RtuSimClient` and `MqttWatcher` helpers.

## Environment Variables

- `MQTT_URL` — Broker address (default: `tcp://localhost:1883`)
- `EDGE_NAME` — Edge identifier
- `EDGE_CONFIG_PATH` — Path to edge config JSON
- `UHN_LOG_LEVEL` — `debug`, `info`, `warn`, `error`
- `LOG_FORMAT` — `json` (default) or `text`
- `UHN_WORKSPACE_PATH` — Sandbox workspace path
- `UHN_SANDBOX_PATH` — Sandbox binary location
