# Dispatch

Reference: DS-1, `docs/dispatch-topology.md`

## Overview

The dispatch daemon replaces the Python `dispatch.py` polling loop with a set of
long-lived goroutines that communicate via channels. MongoDB is written to only for
durable state transitions; all transient coordination lives in memory.

## Goroutines

| Goroutine | Responsibility | Trigger |
|-----------|---------------|---------|
| `dispatchLoop` | Orchestrator. Receives summaries from workers, prints JSON status to stderr. | `time.Ticker` (5s) |
| `ensureLoop` | Bridges QUEUED tasks → PENDING dispatch_requests. Deduplicates. | `time.Ticker` (30s) |
| `reapLoop` | Checks completion markers, PID liveness, stale kills. | `time.Ticker` (10s) |
| `reclaimLoop` | Resets zombie CLAIMED tasks. | `time.Ticker` (60s) |
| `slotCounter` | Channel-served counter. Reads in-memory spawn registry. | On demand |
| `claimLoop` | Claims PENDING requests atomically (findOneAndUpdate). Builds prompt. | On demand (when slots available) |
| `spawnSupervisor` | Forks `otoxan-worker` via MCP-over-stdio. Records PID in registry. | On demand (from claimLoop) |
| `cleanupWorker` | Idempotent reset: task→QUEUED, request→PENDING, spawn→FAILED. | Triggered by reapLoop |

## Shared State

### Spawn Registry

```go
type SpawnRegistry struct {
    mu     sync.RWMutex
    spawns map[string]*SpawnRecord  // key: task_id
    active int                       // cached count for O(1) slot math
}
```

- Writers: `spawnSupervisor` (add), `reapLoop` (update), `cleanupWorker` (reset)
- Readers: `slotCounter`, `dispatchLoop` (status)

### Slot Counter

```go
type SlotCounter struct {
    mu           sync.Mutex
    concurrency  int
    active       int  // RUNNING spawns with live PIDs
    claimed      int  // CLAIMED tasks (transient, not yet spawned)
}
```

Updated by `reapLoop`, `spawnSupervisor`, and `reclaimLoop`. Read by `dispatchLoop` to compute available slots for `claimLoop`.

## Channel Topology

```text
                    ┌─────────────┐
                    │  MongoDB    │
                    │  (tasks,    │
                    │   requests, │
                    │   spawns)   │
                    └──────┬──────┘
                           │
    ┌──────────────────────┼──────────────────────┐
    │                      │                      │
    ▼                      ▼                      ▼
┌──────────┐         ┌──────────┐          ┌──────────┐
│ ensureLoop│         │ claimLoop│          │reapLoop  │
│  (30s)    │         │ (on demand)│        │ (10s)   │
└────┬─────┘         └────┬─────┘          └────┬─────┘
     │                    │                     │
     ▼                    ▼                     ▼
┌──────────┐         ┌──────────┐          ┌──────────┐
│dispatchLoop│◄──────│spawnSupervisor│◄────│cleanupWorker│
│  (5s)    │         │ (fork)   │          │ (reset)  │
└──────────┘         └──────────┘          └──────────┘
     │
     ▼
┌──────────┐
│ slotCounter│
│ (on demand)│
└──────────┘
```

## Task Lifecycle

```text
QUEUED → ensureLoop → PENDING → claimLoop → CLAIMED → spawnSupervisor → RUNNING → reapLoop → COMPLETED
                                                            │
                                                            ▼
                                                      cleanupWorker → FAILED → QUEUED (retry)
```

## Worker Spawn

`spawnSupervisor` forks `otoxan-worker` as a subprocess and communicates via
MCP-over-stdio (JSON-RPC 2.0). The worker receives the task prompt, executes it, and
writes a completion marker. PID liveness is checked by `reapLoop` using `os.FindProcess`
/ `syscall.Kill(0)`.

## Status

The dispatch daemon is under active development. The goroutine topology and channel
contracts are defined; implementation is in progress.
