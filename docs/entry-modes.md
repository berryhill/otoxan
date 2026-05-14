# Entry Modes

> Where am I running and what does that mean for sessions?  
> Canonical reference: DS-7.

---

## 1. Overview

Otoxan supports three entry modes that differ in where the CLI binary runs, where the runtime (MongoDB, Qdrant, dispatch, MCP servers) runs, and what network path connects them. Each mode answers the same question differently: **which session am I attached to, and how do I reconcile state?**

| Mode | Where CLI runs | Where runtime runs | Network | First-class in v1? |
|------|----------------|---------------------|---------|--------------------|
| `local` | Workstation | Workstation | none | **YES** — only mode in v1 |
| `ssh` | User's laptop, attached over SSH | Workstation | SSH | detected for telemetry only |
| `gateway` | User's laptop / phone client | Remote workstation reached via gateway endpoint | HTTPS / gRPC | parked — design only |

This document is the design memo for all three modes. v1 ships `local` only; `ssh` and `gateway` are captured here so the design does not evaporate while the user is parked on them.

---

## 2. Mode: `local` (v1 — first-class)

### 2.1 Definition

Both the otoxan CLI binary and the full runtime (MongoDB, Qdrant, dispatch daemon, MCP servers) run on the same machine — the user's workstation. No network boundary exists between the CLI and the stores it reads and writes.

### 2.2 Session model

- One interactive session per terminal invocation (`otoxan` with no subcommand).
- Session state is stored in MongoDB (`flow_sessions` collection, raw — no soft-delete).
- The CLI attaches to the most recent unclosed session for the current agent, or starts a new one.
- `state.db` (SQLite) is a local ephemeral cache for the dispatch loop only; it is **not** the source of truth for session identity. See `docs/state-layer.md` (DS-1) for the canonical rule: all durable state lives in MongoDB.

### 2.3 Detection heuristic

```go
func DetectEntryMode() EntryMode {
    // Priority: explicit env var > heuristic
    if m := os.Getenv("OTOXAN_ENTRY_MODE"); m != "" {
        return ParseEntryMode(m)
    }
    // local: no SSH_CONNECTION, no gateway headers, binary path under ~/.otoxan/bin/
    if os.Getenv("SSH_CONNECTION") == "" && os.Getenv("OTOXAN_GATEWAY_URL") == "" {
        if strings.HasPrefix(os.Args[0], os.Getenv("HOME")) {
            return ModeLocal
        }
    }
    // ... ssh / gateway heuristics below
}
```

### 2.4 Credential handling

- MongoDB URI read from `config.yaml` or `OTOXAN_MONGO_URI`.
- No remote credential proxy; the binary talks to `localhost:27017` (or whatever the config says).
- Infisical token (if used) is read from env or local config; no cross-machine secret sync.

### 2.5 v1 scope note

This is the only mode that is fully implemented in v1. The other two modes are detected and logged for telemetry, but their session-attachment logic is stubbed and returns an error if invoked.

---

## 3. Mode: `ssh` (parked — design only)

### 3.1 Definition

The otoxan CLI binary runs on the user's laptop, but the terminal session is attached to the workstation via SSH. The runtime (MongoDB, Qdrant, etc.) still runs on the workstation. The network boundary is the SSH tunnel.

### 3.2 Session model

- The SSH client (laptop) does not run the otoxan binary; the binary runs on the workstation.
- Session attachment is identical to `local` from the binary's perspective, because the binary is on the workstation.
- The meaningful difference is **where the human is**: a laptop SSH session may be interrupted (closed laptop, network drop), which raises questions about session lifecycle.

### 3.3 Detection heuristic

```go
if os.Getenv("SSH_CONNECTION") != "" {
    return ModeSSH
}
```

### 3.4 Parked questions

1. **Session survival on disconnect** — if the SSH session drops, should the interactive session be paused, closed, or left running? The dispatch daemon may still be processing tasks; the session state in MongoDB may become stale.
2. **Re-attachment** — when the user SSHs back in, should `otoxan` resume the previous session or start a new one? How do we distinguish "intentional new session" from "reconnect"?
3. **Terminal size / TTY state** — SSH clients may resize terminals; does the interactive session need to propagate `SIGWINCH`?
4. **Agent identity** — if the user SSHs in as a different Unix user, does the agent identity change? The current model keys agents by name, not by Unix UID.

### 3.5 v1 scope note

v1 detects `ModeSSH` for telemetry (logs `entry_mode=ssh` at startup) but does not implement any of the parked questions above. Session behavior is identical to `local`.

---

## 4. Mode: `gateway` (parked — design only)

### 4.1 Definition

The otoxan CLI binary runs on a thin client (user's laptop, phone, or browser). The runtime runs on a remote workstation. The client reaches the runtime via a gateway endpoint (HTTPS or gRPC) rather than direct MongoDB/Qdrant access.

### 4.2 Session model

- The client does **not** hold a MongoDB URI or Infisical token. It holds a short-lived gateway token.
- The gateway authenticates the client, proxies requests to the runtime, and manages session affinity.
- Session state is still stored in MongoDB on the workstation, but the client reads it through the gateway API, not directly.

### 4.3 Detection heuristic

```go
if os.Getenv("OTOXAN_GATEWAY_URL") != "" {
    return ModeGateway
}
```

### 4.4 Parked questions

1. **Gateway protocol** — HTTPS REST, gRPC, or SSE? The MCP servers already speak JSON-RPC 2.0 over stdio; a gateway could bridge to SSE or WebSocket.
2. **Credential proxy** — how does the gateway authenticate clients? JWT? mTLS? How are tokens refreshed?
3. **Session affinity** — if the gateway is load-balanced across multiple workstations, how is a session pinned to the correct runtime?
4. **State.db reconciliation** — the client may cache local state (e.g., offline support). How is `state.db` reconciled with MongoDB when connectivity returns?
5. **Binary split** — does the gateway get its own binary (`otoxan-gateway`) or is it a subcommand of the main CLI? The tentative location is `cmd/otoxan-gateway/` (does not exist yet).
6. **MCP server reachability** — MCP servers currently run as local subprocesses. A gateway mode implies they run remotely; the client needs a transport bridge (SSE over HTTPS, or a custom JSON-RPC proxy).
7. **Dispatch visibility** — in `local` mode the user sees dispatch output in the same terminal. In `gateway` mode, dispatch logs may need to stream to the client over a WebSocket or be stored for later retrieval.

### 4.5 v1 scope note

v1 does not implement the gateway. The `OTOXAN_GATEWAY_URL` env var is ignored (warned, not fatal). No gateway binary exists. This section is a design placeholder.

---

## 5. Mode Detection Scaffold (v1)

### 5.1 Source location

```
internal/entrymode/
    mode.go          // EntryMode enum + ParseEntryMode
    detect.go        // DetectEntryMode() heuristic
    detect_test.go   // unit tests for heuristic
```

### 5.2 Enum definition

```go
package entrymode

type EntryMode int

const (
    ModeUnknown  EntryMode = iota
    ModeLocal              // v1 — fully implemented
    ModeSSH                // detected, telemetry only
    ModeGateway            // detected, stub returns error
)

func (m EntryMode) String() string { ... }
func ParseEntryMode(s string) (EntryMode, error) { ... }
```

### 5.3 Detection rules (priority order)

1. `OTOXAN_ENTRY_MODE` env var — explicit override.
2. `OTOXAN_GATEWAY_URL` set → `ModeGateway`.
3. `SSH_CONNECTION` set → `ModeSSH`.
4. Binary path under `$HOME` and no gateway/SSH signals → `ModeLocal`.
5. Fallback → `ModeUnknown` (logs warning, behaves as `ModeLocal`).

### 5.4 Telemetry hook

`main.go` calls `entrymode.DetectEntryMode()` in `PersistentPreRunE` and logs:

```go
log.Printf("[entry] mode=%s", mode.String())
```

If mode is `ModeGateway`, it logs a warning and exits with a clear message:

```
gateway mode is not implemented in v1; unset OTOXAN_GATEWAY_URL to run locally
```

---

## 6. Cross-Mode Invariants

These rules hold regardless of entry mode so that later modes plug in without rework.

| Invariant | Rationale |
|-----------|-----------|
| MongoDB is the single source of truth for all durable state. | `state.db` is ephemeral; a client reconnecting after a network drop must be able to resume from MongoDB. |
| Agent identity is derived from config, not from the entry mode. | The same agent can be used in `local`, `ssh`, and `gateway` without creating duplicate records. |
| Session IDs are globally unique and mode-agnostic. | A session started in `local` could theoretically be resumed in `gateway` later. |
| The dispatch daemon is always workstation-local. | The client never spawns workers directly; it always requests the daemon. |
| MCP servers are always workstation-local in v1. | Remote MCP is a gateway-mode concern, not a v1 concern. |

---

## 7. References

| Reference | Document |
|-----------|----------|
| DS-1 | `docs/state-layer.md` — Database naming, per-agent isolation |
| DS-3 | `docs/state-layer.md` — Module layout, store packages |
| DS-4 | `docs/state-layer.md` — Index specifications |
| DS-5 | `docs/dispatch.md` — Dispatch goroutine topology, worker spawn |
| DS-7 | This document — Entry mode definitions and detection scaffold |

---

## 8. Changelog

| Date | Change |
|------|--------|
| 2026-05-09 | Initial design memo. `local` is v1-first-class; `ssh` and `gateway` are parked. |
