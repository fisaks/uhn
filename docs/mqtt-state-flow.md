# MQTT Topics & State Flow

This document describes the MQTT topic structure used for communication between the **edge server** (go-uhn) and the **master** (uhn-master), with a focus on how device state flows from physical hardware to the UI.

## Topic Prefix

All edge topics are prefixed with `uhn/{edgeName}/`. The master subscribes using `uhn/+/` wildcards to receive from all edges.

## Three-Tier State Model (P / S / C)

Both the edge (IPCBridge) and the master (StateRuntimeService) maintain identical state semantics per resource:

```
P = Physical State   (from hardware: Modbus poll, IHC notification, or Z2M state)
S = Signal State     (temporary override from master or rule engine)
C = Computed State   (what the runtime and UI see)

C = S !== undefined ? S : P
```

**Rules:**
- P is authoritative, refreshed periodically (Modbus) or on change (IHC)
- S temporarily overrides P while defined
- S clears only when P changes value (not on heartbeat refreshes)
- If both P and S are undefined, state is "unknown"

## State Topics Overview

| Topic Pattern | Direction | Purpose | Payload |
|---|---|---|---|
| `device/{name}/state` | Edge → Master | Modbus device-level state (byte arrays) | `DeviceStatePayload` |
| `device/{name}/pin/{pin}` | Edge → Master | Per-pin physical state (IHC, Z2M). Pin in topic is hex for IHC (`0x9F085E`), literal string for Z2M (`temperature`). | `{ type, pin, value, timestamp }` |
| `device/{name}/availability` | Edge → Master | Per-device online/offline (Z2M devices) | `"online"` or `"offline"` |
| `device/{name}/cmd` | Master → Edge | Direct device commands (set output, toggle) | `{ action, address, value, ... }` |
| `resource/state/{resourceId}` | Edge → Master | Logical resource state (timers, virtual) | `{ resourceId, value, timestamp, details? }` |
| `resource/signal/{resourceId}` | Master → Edge | Signal override | `{ resourceId, value, timestamp }` |
| `resource/cmd/{resourceId}` | Master → Edge | Commands (tap, setState, timer, etc.) | `{ resourceId, action, value?, ... }` |
| `device/{name}/action/{pin}` | Edge → Master | Transient action events (physical button press or rule `emitAction`). Not retained. | `{ action, metadata?, timestamp, depth? }` |

**Key principle:** Physical state topics (`device/`) carry hardware-level data using physical addresses (device name + pin). They have no knowledge of blueprint resource names. The master is responsible for mapping physical addresses to blueprint resource IDs via `makeAddressKey({edge, device, type, pin})`.

## Physical State: Modbus Flow

Modbus devices produce device-level byte arrays (base64-encoded) containing all coils/registers:

```
Modbus Device
  → Poller (polls registers every N ms)
  → BridgedPublisher
    ├→ IPCBridge.HandleDeviceState()         [edge local: update P, recompute C]
    │   └→ ExtractResourceStates()           [decode bytes → per-resource values]
    │       └→ updatePhysicalState()         [P/S/C model]
    │           └→ IPC → Rule Runtime
    └→ EdgeBroker.PublishDeviceState()       [MQTT: device/{name}/state]
        └→ Master: StatePhysicalService
            └→ StateRuntimeService.handlePhysicalState()
                └→ processPins() / processRegisters()
                    └→ makeAddressKey() → resourceId
                        └→ updatePhysicalState()  [P/S/C model]
```

**MQTT payload** (`device/{name}/state`):
```json
{
  "name": "mb-01",
  "timestamp": "2026-03-18T12:00:00Z",
  "timestampMs": 1710763200000,
  "status": "ok",
  "digitalInputs": "AQ==",
  "digitalOutputs": "Ag==",
  "analogInputs": "AKQ=",
  "analogOutputs": "ALg="
}
```

## Physical State: IHC Flow (Per-Pin)

IHC controllers produce individual typed values via SOAP notification long-poll. No byte arrays — each notification carries a single pin's value. The MQTT topic uses the physical address (device + pin), not blueprint resource names.

```
IHC Controller
  → IHCDriver.notificationLoop()            [SOAP long-poll]
  → processEnvelope()                        [typed value: bool/int/float]
  → IPCBridge.UpdatePhysicalStateByAddress()
    ├→ DevicePinStatePublisher               [MQTT: device/{name}/pin/{pin}]
    │   └→ Master: DevicePinStateService
    │       └→ StateRuntimeService.handleDevicePinState()
    │           └→ makeAddressKey() → resourceId
    │               └→ updatePhysicalState()  [P/S/C model]
    └→ ResourceMap lookup                    [device+type+pin → resourceId]
        └→ updatePhysicalState()             [edge local: P/S/C model]
            └→ IPC → Rule Runtime
```

**MQTT publish is independent of blueprint:** The edge publishes pin state as soon as it receives it from the hardware, even if no blueprint is active. The local runtime state update only happens when a ResourceMap exists (active blueprint).

**MQTT payload** (`device/ihc2/pin/0x9F085E`):
```json
{
  "type": "digitalOutput",
  "pin": 10422366,
  "value": true,
  "timestamp": 1710763200000
}
```

The topic uses hex (`0x9F085E`) for readability (matches IHC Visual project files). The payload `pin` stays as an integer for machine processing. The pin in the topic is not used in any logic — only the `device` segment is parsed.

Both Modbus and IHC paths converge to the same `updatePhysicalState(resourceId, value, timestamp)` on the master. The master resolves physical addresses to resource IDs via `makeAddressKey()` — the physical layer never needs to know about blueprint resource names.

## Physical State: Zigbee/Z2M Flow (Per-Pin, Filtered)

Zigbee devices are managed via Zigbee2MQTT (Z2M), which runs as a Docker container on the edge. Z2M handles the Zigbee radio protocol, device pairing, and converter logic. The edge subscribes to Z2M's MQTT topics using raw (unprefixed) subscriptions — Z2M publishes to `zigbee2mqtt/...`, not under the `uhn/{edge}/` namespace.

```
Z2M (Docker container) → zigbee2mqtt/{device} (raw MQTT, full JSON blob)
  → ZigbeeTransport.handleZ2MDeviceState()
    → Cache raw blob for replay (before ResourceMap exists)
    → Skip if no ResourceMap yet (state replayed later via onResourceMapReady)
    → processStateBlob() per property:
      ├→ Filter: only properties in ResourceMap (blueprint-exported)
      ├→ Round: decimalPrecision from resource definition
      ├→ Change detection: skip if value unchanged
      └→ IPCBridge.UpdatePhysicalStateByAddress()
          ├→ DevicePinStatePublisher   [MQTT: device/{name}/pin/{pin}]
          │   └→ Master: same path as IHC per-pin
          └→ ResourceMap lookup → updatePhysicalState() [edge local: P/S/C]
```

**Key differences from IHC:**
- **Filtered by ResourceMap** — Z2M devices expose many properties (linkquality, battery, etc.) but only blueprint-exported properties are published. IHC publishes all notifications unconditionally.
- **Filtered by edge config** — only devices listed in `zigbee[].devices[]` are processed. Unlisted Z2M devices are ignored entirely.
- **String pins** — Z2M uses property names as pins (`"temperature"`, `"state"`, `"power"`), not numeric IDs. The MQTT topic uses the literal string: `device/kitchen_temperature_display/pin/temperature`.
- **`decimalPrecision` rounding** — analog values are rounded before change detection to reduce noise from sensors that report tiny fluctuations.
- **Cached blob replay** — Z2M retained messages arrive before the ResourceMap is built. Blobs are cached per device and replayed through the filtered path once the ResourceMap is ready.
- **`BypassSignalState() = false`** — Z2M state flows back through MQTT naturally, so the standard S/P model applies (unlike IHC where signals bypass local state).
- **`optimistic: false` recommended** — Z2M's optimistic mode can cause state flip-flop on devices with frequent property reports (energy monitoring). Configured per device in edge config.
- **Raw MQTT** — Z2M subscriptions and `/set` commands use `broker.SubscribeRaw()` / `broker.PublishRaw()` to bypass the `uhn/{edge}/` prefix.

**MQTT payload** (`device/kitchen_temperature_display/pin/temperature`):
```json
{
  "type": "analogInput",
  "pin": "temperature",
  "value": 22.5,
  "timestamp": 1710763200000
}
```

**Z2M `/set` commands** (toggle smart plug):
```
ZigbeeDriver.SetOutput("state", false)
  → bool→"OFF" conversion
  → PublishRaw: zigbee2mqtt/socket_plug_1/set {"state": "OFF"}
  → Z2M sends Zigbee command → device confirms
  → Z2M publishes: zigbee2mqtt/socket_plug_1 {"state": "OFF", ...}
  → Transport processes confirmed state
```

**Device availability:**
Z2M publishes `zigbee2mqtt/{device}/availability` → transport publishes to `device/{name}/availability` on UHN MQTT (retained). Only for config-listed devices.

## Physical State: Mi-Light Flow (Assumed State)

Mi-Light devices use iBox2 UDP v6 protocol — **one-way commands with ACK but no state feedback**. The edge publishes **assumed state**: after a successful command+ACK, the commanded value is published as P via `UpdatePhysicalStateByAddress()`.

```
Blueprint Rule / View Command
  → MilightDriver.SetOutput(pin, value)
  → buildCommands() → []transportCommand (one or more [9]byte UDP commands)
  → MilightTransport.enqueue()
  → runLoop dequeues command
  → checkGatekeeper(zone)                        [reads PhysicalStateReader cache]
    ├→ Gatekeeper OFF → reconfirm current physical state (re-publish P with new timestamp)
    │   └→ UI receives same value with updated timestamp → optimistic slider/state snaps back
    └→ Gatekeeper ON / unknown / no gatekeeper → proceed
  → UDPClient.SendCommand() → ACK (0x88)
  → IPCBridge.UpdatePhysicalStateByAddress()    [assumed P]
    ├→ DevicePinStatePublisher                   [MQTT: device/{name}/pin/{pin}]
    │   └→ Master: same path as IHC per-pin
    └→ ResourceMap lookup → updatePhysicalState() [edge local: P/S/C]
  → publish sideEffects (e.g. power off resets night mode state)
```

**Key differences from IHC/Modbus:**
- **No independent state source** — state is only updated when we send a command. No polling, no notification loop.
- **`assumedState: true`** in catalog — UI can indicate that displayed state is "best guess" rather than confirmed hardware state.
- **`BypassSignalState() = true`** — signals forwarded to driver → UDP command → assumed P published. Without this, S would stick forever since there's no independent P update to clear it.
- **No state on startup** — state is unknown until the first command. Stale assumptions are not published.
- **Per-bridge command queue** — iBox2 requires minimum 100ms between UDP sends. All zones on one bridge share a serialized queue.
- **Health via ACK** — UDP v6 acknowledges every command (0x88). No ACK after retries (default 1) = command fails. Retries are immediate with no backoff delay.
- **Side effects** — some commands affect other pins' assumed state (e.g. power off resets night mode to false).

**Resource mapping** (FUT069 example, per zone e.g. `device/milight-toilet`):
| Pin | Type | Value | Description |
|-----|------|-------|-------------|
| 0 | digitalOutput | bool | Power on/off |
| 1 | digitalOutput | bool | Night mode (on = enter, off = power cycle exit) |
| 2 | digitalInput (push) | bool | White mode (fire-and-forget) |
| 3 | digitalInput (push) | bool | Speed up (fire-and-forget) |
| 4 | digitalInput (push) | bool | Speed down (fire-and-forget) |
| 5 | analogOutput | 0–100 | Brightness (%) |
| 6 | analogOutput | 0–100 | Color temperature (warm→cool) |
| 7 | analogOutput | 0–255 | Hue — enters color mode |
| 8 | analogOutput | 0–100 | Saturation (%) |
| 9 | analogOutput | 1–9 | Effect mode |

Pin layout is fixed per model (configured via `model` field in zone config).

## Action Event Flow

Action events are **transient, fire-and-forget** events that bypass the P/S/C state model entirely. They are used for Zigbee button presses where the `action` property (e.g. `toggle`, `brightness_step_up`, `arrow_left_click`) represents a momentary event, not a persistent state.

### Physical button press (Z2M → edge → master)

```
Zigbee Button Press
  → Z2M publishes: zigbee2mqtt/{device} {"action": "toggle", "action_duration": 0.12, ...}
  → ZigbeeTransport.handleZ2MDeviceState()
    → Detect `action` field in Z2M blob (non-empty string)
    → Skip if MQTT retained flag is set (stale action from broker restart)
    → Collect action metadata from sibling `action_*` fields (e.g. action_duration)
    → ActionEventEmitter.EmitActionEvent(device, pin, action, metadata, timestamp)
      ├→ IPCBridge: IPC to edge rule runtime (actionEvent message)
      └→ EdgeBroker: MQTT publish to device/{name}/action/{pin} (QoS 1, NOT retained)
          └→ Master: ActionEventService subscribes
              └→ IPC to master rule runtime (actionEvent message)
```

### UI action command (master → edge)

```
UI Action → WebSocket → Master
  ├→ IPC to master rule runtime (actionEvent message)
  └→ MQTT publish to resource/cmd/{resourceId} {action: "actionInput", value: "toggle"}
      → Edge: ResourceCmdSubscriber
          → IPC to edge rule runtime (actionEvent message)
```

### Rule-emitted action (emitAction)

Rules can emit action events targeting `actionInput` resources via `ruleAction({ type: "emitAction", ... })`. This enables rule chaining — e.g. PIR detects motion → rule emits action on button resource → button's rules fire.

```
Rule fires emitAction
  → Rule engine: toRuntimeAction() increments depth (cause.depth + 1)
  → Early loop prevention: same resource+action as cause → dropped
  → IPC → Host (edge or master)

Edge host:
  → EdgeActionHandler.handleEmitAction()
    → Validate depth < 10 (MaxActionDepth)
    ├→ IPC to local edge runtime (actionEvent with depth)
    └→ MQTT: device/{device}/action/{pin} (edge → master, same as physical Z2M actions)
        └→ Master: ActionEventDispatcher
            └→ IPC to master runtime (actionEvent with depth)

Master host:
  → RuleActionDispatcher.handleEmitActionAction()
    → Validate depth < 10
    ├→ IPC to local master runtime (actionEvent with depth)
    └→ MQTT: resource/cmd/{resourceId} {action: "action", depth} (master → edge)
        └→ Edge: ResourceCmdSubscriber → forwardActionCommand
            └→ IPC to edge runtime (actionEvent with depth)
```

**Loop prevention (three layers):**
1. **Same-cause rejection** — rule engine drops `emitAction` targeting same resource+action as the triggering cause (immediate, in-process)
2. **Depth counter** — incremented on each rule-emitted hop, host rejects at depth ≥ 10
3. **Topic direction** — edge publishes to `device/*/action/*` (edge → master), not `resource/cmd/` (master → edge), preventing self-echo

### Key design points

- **No P/S/C involvement** — actions are events, not state. They don't update physical state, signal state, or computed state.
- **Retained messages filtered** — Z2M publishes state with `retain: true`, which means the last action value persists in the broker. The transport checks the MQTT retained flag and skips any retained action to prevent stale button presses from replaying on reconnect.
- **Empty actions skipped** — Z2M sometimes publishes `"action": ""` when clearing the action field. These are ignored.
- **Action metadata** — sibling fields matching `action_*` (e.g. `action_duration`, `action_rate`) are collected into a metadata map and included in the event payload. Other state properties in the same blob are processed normally through the per-pin state path.
- **Pin is the action property name** — typically `"action"` for most Zigbee devices, matching the Z2M expose name. The topic uses the literal string: `device/button_panel/action/action`.

## Logical Resource State

Logical resources (timers, complex, virtualDigitalInput, virtualAnalogOutput) bypass the P/S/C model entirely. Their state is authoritative — no signal override.

```
Rule Runtime
  → logicalResourceStateChanged event
  → IPCBridge
    ├→ Update local computedState directly
    └→ LogicalResourceStatePublisher         [MQTT: resource/state/{resourceId}]
        └→ Master: LogicalResourceStateService
            └→ StateRuntimeService.handleLogicalResourceState()
                └→ Set C = value directly (single-tier, no P/S)
```

## Signal Override Flow

Signals temporarily override physical state. Used for manual control from the UI or rule-driven overrides.

### Default (Modbus and non-driver resources)

```
Master UI / Rule Action
  → Master publishes to resource/signal/{resourceId}
  → Edge: ResourceSignalSubscriber
    → IPCBridge.HandleSignalUpdate()
      → S = value, recompute C = S ?? P
      → IPC → Rule Runtime
```

Signal is cleared when physical state changes (not on periodic refreshes).

### IHC Signal Forwarding (bypassSignalState)

IHC inputs (buttons, PIRs) are **writable** — unlike Modbus where inputs are read-only. When the master sends a signal override for an IHC resource, the edge forwards it to the IHC controller instead of setting local signal state (S). The controller processes it through its function blocks, and the resulting state flows back as physical state (P) through the notification loop.

```
Master UI / Rule Action
  → Master publishes to resource/signal/{resourceId}
  → Edge: ResourceSignalSubscriber
    → Lookup resource in ResourceMap → find device driver
    → driver.BypassSignalState() == true?
      YES → driver.HandleSignal(pin, value)           [SOAP setResourceValue]
            → IHC controller processes function blocks
            → Notification loop receives state changes
            → IPCBridge.UpdatePhysicalStateByAddress() [P updates, S never set]
      NO  → IPCBridge.HandleSignalUpdate()             [default: set S, recompute C]
```

**Why not set S for IHC?** Because `C = S ?? P` — if S were set, it would mask the real physical state that comes back from the controller. IHC is the source of truth. The signal is just a command to the controller; the authoritative state comes back through notifications.

This behavior is determined by `DeviceDriver.BypassSignalState()`. Any future driver that manages its own state loop (like IHC) returns `true` and gets the same forwarding behavior automatically.

## Command Flow

Commands from the master trigger actions on the edge through two paths:

### Logical resource commands (from rule runtime)
```
Rule Runtime → IPCBridge → ActionHandler
  ├→ setDigitalOutput / setAnalogOutput:
  │   ├→ virtualAnalogOutput? → HandleSetState() (local state + MQTT publish)
  │   ├→ DeviceDriver exists? → driver.SetOutput() (IHC: SOAP setResourceValue)
  │   └→ Fall back to bus pollers (Modbus: WriteRegister)
  ├→ emitSignal:
  │   ├→ DeviceDriver.BypassSignalState()? → driver.HandleSignal() (IHC: SOAP)
  │   └→ Fall back to local signal state (Modbus)
  └→ emitAction:
      → Validate depth < 10
      ├→ IPC to local runtime (actionEvent with depth)
      └→ MQTT: device/{device}/action/{pin} → master ActionEventDispatcher
```

### Device commands (from master UI direct click on resource)
```
Master → device/{name}/cmd                  [setdigitaloutput, setanalogoutput]
  → Edge: SystemCommandHandler → DeviceCommandHandler
    ├→ DeviceDriver exists? → driver.SetOutput() (IHC: SOAP setResourceValue)
    │   Note: IHC digitalOutput writes may conflict with IHC function blocks for a short time
    └→ Fall back to bus pollers (Modbus)
```

### View commands (tap / longPress from InteractionView)
```
Master → resource/cmd/{resourceId}          [tap, longPress, setState, timer]
  → Edge: ResourceCmdSubscriber
    → See "View Command Flow" section below
```

## View Command Flow (Tap / Long Press)

When a user taps or long-presses a button in the UI (InteractionView), the system must produce the **same events** the rule runtime sees from a physical button press: `activated, deactivated, tap` (or `activated, longPress, deactivated`). This requires coordinated handling on both the master and edge.

### How it works

The master uses the `bypassSignalState` flag from the edge catalog to decide the routing strategy. The edge uses its local `DeviceDriver` registry for the same purpose.

For long press, the view's `longPress` command has an optional **`simulateHold`** flag (default `false`). This controls whether the long press is simulated as a physical hold (state cycle through `InputGestureEmitter`) or forwarded as a direct `longPressCommand` to the rule runtime:

- **`simulateHold: false`** (default) — `longPressCommand` forwarded to both edge and master runtimes. Rules see a single `longPress` event instantly. No activated/deactivated cycle.
- **`simulateHold: true`** — synthetic hold for thresholdMs+buffer, producing `activated, longPress, deactivated` through `InputGestureEmitter`. Matches real physical button behavior exactly.

**Key constants:**
- `PRESS_DURATION_MS = 50` — delay between synthetic activate and deactivate for tap
- `LONG_PRESS_BUFFER_MS = 100` — extra ms added to longPress thresholdMs so the runtime's `setTimeout` fires before the synthetic release
- `VIRTUAL_PRESS_DURATION_MS = 300` — delay for virtualDigitalInput/complex press pulse (longer for visual feedback)

### Tap from View

| Resource type | Device | Edge runtime events | Master runtime events | Mechanism |
|---|---|---|---|---|
| `digitalInput` (push) | IHC | activated, deactivated, tap | activated, deactivated, tap | Edge auto-pulses driver → physical state round-trip back to edge and master |
| `digitalInput` (push) | Modbus | activated, deactivated, tap | activated, deactivated, tap | Both inject synthetic stateUpdate(true→false) |
| `complex` | any | activated, deactivated, tap | activated, deactivated, tap | Synthetic stateUpdate + explicit tapCommand |
| `virtualDigitalInput` (push) | any | activated, deactivated, tap | activated, deactivated, tap | Synthetic stateUpdate + explicit tapCommand |

For `digitalInput`: `InputGestureEmitter` auto-detects tap from the state cycle (press < 1000ms) — no explicit `tapCommand` needed.
For `complex` / `virtualDigitalInput`: `InputGestureEmitter` ignores them — explicit `tapCommand` required alongside synthetic state.

**Event order:** `activated, longPress, deactivated` — the `longPress` event fires while the button is still held (setTimeout expires during hold), before the `deactivated` on release. This differs from tap where `deactivated` comes before `tap`.

### Long Press from View

#### Default (`simulateHold: false`)

| Resource type | Device | Edge runtime events | Master runtime events | Mechanism |
|---|---|---|---|---|
| any | any | longPress only | longPress only | Explicit `longPressCommand` forwarded to both runtimes — no activated/deactivated/stateChange events |

This is the simple path: master sends `longPressCommand` to both edge and master runtimes. Rules that use `.onLongPress()` fire immediately. No state cycle, no `InputGestureEmitter` involvement.

#### With `simulateHold: true`

| Resource type | Device | Edge runtime events | Master runtime events | Mechanism |
|---|---|---|---|---|
| `digitalInput` (push) | IHC | activated, longPress, deactivated | activated, longPress, deactivated | Edge holds driver signal for thresholdMs+buffer → physical state round-trip back to edge and master |
| `digitalInput` (push) | Modbus | activated, longPress, deactivated | activated, longPress, deactivated | Both inject synthetic stateUpdate with timed delay |
| Other types | any | longPress only | longPress only | Falls back to `longPressCommand` (simulateHold ignored for non-digitalInput) |

Use `simulateHold` when the rule needs the full `activated → longPress → deactivated` state cycle — for example, when the same rule handles both physical buttons and UI buttons and relies on `InputGestureEmitter` timing. Trade-off: `simulateHold` introduces a delay (thresholdMs + 100ms buffer) between the UI press and the rule executing, since the runtime must wait for the hold timer to expire before emitting the `longPress` event.

### Detailed flow: `digitalInput` tap

```
UI Tap → WebSocket → Master CommandsResourceService.handleDigitalInput()
  │
  ├─ bypassSignalState = true (IHC):
  │   └→ resource/cmd/{id} → Edge ResourceCmdSubscriber
  │       └→ autoPulseDriver: HandleSignal(true), wait 50ms, HandleSignal(false)
  │           └→ IHC controller processes → notification loop
  │               ├→ Edge: UpdatePhysicalStateByAddress → stateUpdate(true), stateUpdate(false)
  │               │   └→ InputGestureEmitter → activated, deactivated, tap
  │               └→ MQTT: device/{name}/pin/{pin} → Master
  │                   └→ updatePhysicalState → stateUpdate(true), stateUpdate(false)
  │                       └→ InputGestureEmitter → activated, deactivated, tap
  │
  └─ bypassSignalState = false (Modbus):
      ├→ Master: inject synthetic stateUpdate(true), wait 50ms, stateUpdate(false)
      │   └→ InputGestureEmitter → activated, deactivated, tap
      └→ resource/cmd/{id} → Edge ResourceCmdSubscriber
          └→ inject synthetic stateUpdate(true), wait 50ms, stateUpdate(false)
              └→ InputGestureEmitter → activated, deactivated, tap
```

### Detailed flow: `digitalInput` longPress

```
UI LongPress → WebSocket → Master CommandsResourceService

simulateHold = false (default):
  ├→ resource/cmd/{id} {action: "longPress", durationMs} → Edge
  │   └→ forwardLongPressCommand → runtime longPress event
  └→ Master runtime: longPressCommand → longPress event
  (Both runtimes see longPress instantly, no state cycle)

simulateHold = true:
  │
  ├─ bypassSignalState = true (IHC):
  │   └→ resource/cmd/{id} {action: "longPress", durationMs, simulateHold: true} → Edge
  │       └→ holdDriverSignal: HandleSignal(true), wait thresholdMs+100ms, HandleSignal(false)
  │           └→ Physical round-trip → activated, longPress, deactivated (both runtimes)
  │   (Master does NOT inject synthetic state — physical round-trip handles both sides)
  │
  └─ bypassSignalState = false (Modbus):
      ├→ Master: inject stateUpdate(true), wait thresholdMs+100ms, stateUpdate(false)
      │   └→ InputGestureEmitter → activated, longPress, deactivated
      └→ resource/cmd/{id} {simulateHold: true} → Edge
          └→ inject stateUpdate(true), wait thresholdMs+100ms, stateUpdate(false)
              └→ InputGestureEmitter → activated, longPress, deactivated
```

### Why the buffer?

The runtime's `InputGestureEmitter` uses `setTimeout(thresholdMs)` to detect long press. The synthetic release must arrive AFTER this timeout fires. Adding `LONG_PRESS_BUFFER_MS (100ms)` to the hold duration ensures the deactivate arrives after the longPress event.

### Edge catalog `bypassSignalState` flag

The master determines device capabilities from the edge catalog:

```json
{
  "name": "ihc2",
  "type": "ihc",
  "bypassSignalState": true,
  "resources": [
    { "id": 10422366, "type": "digitalOutput" },
    { "id": 10420316, "type": "digitalInput" }
  ]
}
```

Z2M devices use string IDs:
```json
{
  "name": "kitchen_temperature_display",
  "type": "zigbee",
  "resources": [
    { "id": "temperature", "type": "analogInput" },
    { "id": "humidity", "type": "analogInput" },
    { "id": "battery", "type": "analogInput" }
  ]
}
```

- `bypassSignalState: true` — signals bypass the S (signal) tier and go directly to the device driver. Driver manages state via notification loop (IHC). Master sends commands to edge only, does NOT inject synthetic state.
- `bypassSignalState: false` (or absent) — externally polled (Modbus) or event-driven (Z2M). Master injects synthetic state locally AND sends to edge.
- `resources` — list of valid resource IDs (numeric for IHC, string for Z2M). Used by the master to validate blueprint pin references.

## Other Topics

| Topic | Direction | Purpose |
|---|---|---|
| `status` | Edge → Master | Edge online/offline status |
| `identity` | Edge → Master | Edge public key for auth |
| `catalog` | Edge → Master | Device catalog (available hardware, republished on Z2M discovery) |
| `runtime/status` | Edge → Master | Rule runtime status (running/stopped/error) |
| `runtime/rules` | Edge → Master | Count of loaded rules |
| `blueprint/activated` | Both | Active blueprint info |
| `system/config` | Edge → Master | Edge system configuration |
| `mute/event` | Edge → Master | Mute state changes |
| `mute/cmd` | Master → Edge | Mute commands |

## Adding a New Driver

When adding a new hardware protocol:

1. Implement `DeviceDriver` interface (`HandleSignal`, `SetOutput`, `BypassSignalState`)
   - Pin type is `any` — numeric (IHC, Modbus) or string (Z2M)
2. Call `IPCBridge.UpdatePhysicalStateByAddress(ctx, device, type, pin, value, timestamp)` for state updates
   - Callers are responsible for pre-filtering (Z2M filters by ResourceMap, IHC publishes all)
3. State automatically flows to both:
   - Edge runtime (local IPC via ResourceMap lookup)
   - Master (MQTT `device/{name}/pin/{pin}` → address-to-resourceId mapping)
4. Add device to edge catalog so master recognizes it:
   - Set `BypassSignalState: true` if the driver manages its own state loop
   - Include `Resources` list with `id` (numeric or string) for master-side pin validation
5. Register driver in `EdgeActionHandler` for command dispatch
6. Wire `resourceCmdSub.SetDrivers(drivers)` and `resourceSignalSub.SetDrivers(drivers)` in `main.go`
7. View tap/longPress will automatically use the correct strategy based on `BypassSignalState`
8. For MQTT-based protocols (like Z2M): use `broker.SubscribeRaw()` / `broker.PublishRaw()` for topics outside the `uhn/{edge}/` namespace
9. For devices with transient action events (buttons): implement `ActionEventEmitter` to emit events via IPC + MQTT `device/{name}/action/{pin}`. Filter retained messages to prevent stale actions on reconnect.
