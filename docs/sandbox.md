# UHN Sandbox Design & Guarantees

## Overview

UHN executes user-authored automation rules inside a **Linux sandbox** to ensure safety, stability, and isolation from the host system.

The sandbox is designed to:

- prevent accidental or unintended damage to the host
- contain runaway or buggy rule code
- enforce resource limits
- provide predictable runtime behavior
- remain simple, auditable, and maintainable

This document describes **what the sandbox guarantees**, **what it does not**, and **why specific design choices were made**.

---

## Threat Model

The UHN sandbox is designed for **single-tenant home automation**:

- The runtime executes code authored by the system owner
- The primary goal is to protect the **host system**
- The sandbox must tolerate bugs, infinite loops, memory leaks, and misuse of APIs

This is **not** a cloud-grade multi-tenant security boundary.

---

## High-Level Architecture

Sandbox execution is split into **four clearly separated roles**, each with a strict responsibility boundary:

1. **uhn-master** (Node.js, unprivileged)
   - Orchestrates the overall system
   - Generates sandbox configuration
   - Communicates with the runtime via explicit IPC
   - Does not perform privileged operations
   - Does not execute sandboxed code

2. **uhn-sandbox-launcher** (unprivileged)
   - Entry point for sandbox execution
   - Validates arguments and configuration paths
   - Spawns the privileged setup process
   - Remains alive for the entire sandbox lifetime
   - Exits when the sandbox terminates

3. **uhn-sandbox-setup** (Go, runs as root briefly)
   - Applies cgroup limits
   - Creates Linux namespaces
   - Constructs the sandbox filesystem
   - Performs `chroot()`
   - Drops privileges permanently
   - `exec()`s the sandbox supervisor in normal operation
   - Swap the setup process with sandboxd binary (same PID, new image) in normal operation.
   - **Remains alive as root only when `network: "debug-attach"` is used**, acting as a host-side debug forwarder port

4. **uhn-sandboxd** (Go, unprivileged)
   - Acts as the sandbox **supervisor**
   - Launches and monitors the rule runtime
   - Manages process groups and signal forwarding
   - Does **not** execute user-authored rules
   - Does **not** communicate directly with uhn-master

5. **uhn-runtime** (Node.js, unprivileged)
   - Executes user-authored automation rules
   - Implements the rule engine
   - Communicates with uhn-master via explicit IPC
   - Runs fully inside the sandbox under sandboxd supervision

Only **uhn-sandbox-setup** runs as root, and only until the sandbox is fully constructed.
After `exec()`, no privileged process remains except in explicit debug-attach mode.

---

## Security Guarantees

### Filesystem Isolation

- The sandbox runs in a **private mount namespace**
- The process is confined using **`chroot()`**
- The sandbox root filesystem is **explicitly constructed**
- Only explicitly bind-mounted paths exist inside the sandbox
- Host filesystem paths are not reachable except where intentionally mounted

Filesystem behavior:

- System directories (`/bin`, `/lib`, `/usr/bin`, etc.) are bind-mounted **read-only**
- Runtime code is mounted from explicit host paths
- The sandbox root itself is not writable

Writable locations are intentionally limited to:

- `/tmp` (tmpfs)

The `/dev` filesystem is mounted as a `tmpfs`, populated with a minimal and explicit
set of character devices (`null`, `zero`, `random`, `urandom`), and then
**remounted read-only**. Sandboxed processes cannot create, remove, or modify
device nodes.

---

### Privilege Isolation

- The sandbox runtime executes as a **non-root user**
- All supplementary groups are dropped
- Privileges are permanently dropped via `setuid` / `setgid`
- `PR_SET_NO_NEW_PRIVS` is enforced
- No Linux capabilities are granted

Once privileges are dropped, they **cannot be regained**.

---

### Resource Limits

The sandbox uses **cgroups v2** to enforce:

- Memory limits (`memory.max`)
- Process count limits (`pids.max`)

This prevents:

- fork bombs
- runaway memory usage
- host resource exhaustion

---

### Process Visibility

- The sandbox does **not** use PID namespaces
- Processes may observe numeric host PIDs via `/proc`
- The runtime cannot signal, trace, or interfere with host processes

PID visibility is informational only and does not grant authority.

---

### `/proc` Handling

- A fresh `/proc` filesystem is mounted inside the sandbox
- Mounted with `nosuid`, `noexec`, and `nodev`
- Prevents access to device-backed or executable paths

The `/proc` mount reflects only what the kernel exposes to the sandboxed process.

---

### Network Isolation

The sandbox uses **Linux network namespaces** to strictly control network access.

By default:

- The sandbox runs in a **private network namespace**
- No external network interfaces are present
- No outbound or inbound network access exists
- Only the loopback interface may be enabled (depending on configuration)

---

### Network Modes

#### `network: "none"`

- A private network namespace is created
- No network interfaces are enabled
- All network access is disabled

#### `network: "lo"` 

- A private network namespace is created
- Only the loopback interface (`lo`) is enabled
- No external connectivity is possible

#### `network: "full"`

- No network namespace is created
- The sandbox shares the host network stack
- Full network access is available

#### `network: "debug-attach"`

This mode exists **exclusively for debugging**.

- The sandbox runs in a private network namespace
- Only loopback is enabled inside the sandbox
- The sandbox has no external network access
- A host-side debug forwarder bridges connections into the sandbox

---

### Debug Listen Address

When using `network: "debug-attach"`:

```
debugListen: "0.0.0.0:9250"
```

- Controls where the host-side debug forwarder listens
- The sandbox itself always listens on `127.0.0.1`
- Firewalling and access control are the responsibility of the host

---

## Lifecycle & Supervision Guarantees

- **uhn-sandbox-launcher** remains alive for the entire sandbox lifetime
- **uhn-sandboxd** is always a descendant of the launcher
- **uhn-runtime** always runs under sandboxd supervision
- If the launcher exits, the sandbox is terminated
- If the runtime exits, the launcher exits with the same status
- All processes spawned by uhn-sandboxd run in a dedicated process group.
- Signals are forwarded and process trees are terminated deterministically

This guarantees a clean and observable lifecycle without orphaned processes.

---

## What the Sandbox Does *Not* Guarantee

The sandbox intentionally does **not** provide:

- Multi-tenant isolation
- If the kernel itself has a vulnerability, the sandbox does not try to stop an attacker from exploiting it.
- Complete concealment of host process existence
- Deterministic timing or real-time execution guarantees
- Defense against malicious code designed to exploit the kernel

These are out of scope for UHN’s deployment model.

---

## Design Decisions & Rationale

### No PID Namespaces

PID namespaces are intentionally not used:

- They significantly complicate supervision semantics
- They interact poorly with Go’s execution model
- They risk breaking the supervisor model (launcher → sandboxd → runtime).
- PID visibility alone does not grant control
- `pids.max` cgroup limits already cap damage

This preserves a simple and reliable lifecycle relationship.

---

### No Shell Usage

The sandbox never invokes a shell:

- No environment-based command expansion
- No globbing
- No implicit command parsing

All execution uses `execve` semantics with explicit arguments.

---

### Executable Exposure

The sandbox runtime and supervisor are exposed via **explicit bind-mounted executable paths**.

- No writable executable locations
- No dynamic executable discovery
- No file descriptor–based execution

This keeps execution paths explicit and auditable.

---

### pnpm and Development Mode

pnpm uses symlink-heavy `node_modules` layouts that are not compatible with minimal chroot environments.

UHN handles this distinction **outside of the sandbox**:

- `uhn-master` detects whether the system is running in development or production mode
- Based on this, it generates and passes an explicit sandbox configuration
- In **development mode**, `uhn-master` may expose a broader set of filesystem paths (e.g. the full monorepo) so that tooling and dependencies resolve correctly
- In **production mode**, only the minimal set of runtime artifacts is present on the host and therefore exposed to the sandbox

The sandbox implementation itself does **not** change between development and production; it strictly enforces the configuration it is given.

---

## Sandbox Contract (Invariants)

The following conditions must always hold:

- No writes to host filesystems
- No unintended network access
- No privilege escalation
- All runtime paths are sandbox-relative
- The launcher acts as a lifetime anchor; uhn-master supervises and restarts the sandbox as needed.
- sandboxd never executes user-authored rule code

Any change violating these invariants is a **security regression**.

---

## Pipe-based IO in the Sandbox

### Why the sandbox uses pipe-based IO (no TTY / PTY)

The sandbox supervisor intentionally uses **pipe-based stdin/stdout/stderr forwarding**
instead of attaching sandboxed processes directly to the host terminal or allocating
a PTY.

This is a **deliberate design choice** to keep the sandbox **predictable, secure,
and easy to reason about** under strict isolation.

---

### What this means in practice

Sandboxed processes:

- Do **not** run with a controlling terminal
- Are **non-interactive** by default
- Receive input and produce output via pipes
- Do **not** support job control, readline, or full-screen TUI tools

This behavior is intentional.

---

### Why the host terminal is not attached directly

Directly connecting the sandboxed process to the host terminal:

```go
cmd.Stdin  = os.Stdin
cmd.Stdout = os.Stdout
cmd.Stderr = os.Stderr
```

creates **ambiguous execution semantics** inside a sandbox:

- The process may *appear* interactive
- No controlling TTY is actually available
- Signal delivery becomes unreliable
- Some programs block or fail unexpectedly

This “half-interactive” mode is fragile and difficult to reason about when using
namespaces, mount restrictions, and other sandboxing mechanisms.

---

### Why a PTY is not allocated by default

Allocating a PTY requires **explicit policy changes**, including:

- Mounting `devpts`
- Providing `/dev/ptmx`
- Allowing session leadership (`setsid`)
- Allowing execution of system binaries in interactive contexts
- Potentially relaxing other sandbox restrictions

While PTYs are useful for **fully interactive debugging**, they are not required
for normal sandbox operation and significantly increase complexity.

---


### Interactive debugging

For debugging purposes, it is possible to execute a `bash` shell inside the
sandbox and inspect the environment or issue simple commands.

This debug shell runs using the same **pipe-based IO model** as normal sandbox
processes. As a result:

- Input and output are forwarded via pipes, not a terminal
- There is no job control or readline support
- Signal handling is minimal
- Output formatting may appear **raw or unpolished**

This mode is intended for **basic inspection and troubleshooting**, not for
full interactive terminal usage.

---

## Summary

The UHN sandbox is intentionally:

- minimal
- explicit
- conservative
- auditable

It prioritizes correctness and clarity over maximal isolation and avoids complex mechanisms unless they provide clear security value.

---

