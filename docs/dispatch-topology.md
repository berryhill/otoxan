# Dispatch Topology: Python → Go Goroutine Mapping

Reference: DS-1  
Scope: `cmd/otoxan-dispatch` (dispatcher daemon) + `cmd/otoxan-worker` (MCP worker)

---

## 1. Executive Summary

The Python dispatcher (`~/.hermes/scripts/dispatch.py`) is a single-threaded, polling-based script that runs a linear pipeline every invocation. The Go version replaces this with a set of long-lived goroutines that communicate via channels. MongoDB is written to only for durable state transitions; all transient coordination (slot counting, spawn tracking, completion detection) lives in memory.

---

## 2. Python → Go Goroutine Mapping Table

| Python Function | Go Goroutine | Trigger | Input Channel(s) | Output Channel(s) | Notes |
|-----------------|--------------|---------|------------------|-------------------|-------|
| `main()` (loop driver) | `dispatchLoop` | `time.Ticker` (5s) or `runtime.GOMAXPROCS` | `reapDone`, `claimDone`, `spawnDone`, `statusReq` | `statusResp`, `shutdown` | Orchestrator. Receives summaries from workers, prints JSON status to stderr. |
| `ensure_dispatch_requests()` | `ensureLoop` | `time.Ticker` (30s) | `ensureTick` | `ensureDone` → `dispatchLoop` | Bridges QUEUED tasks → PENDING dispatch_requests. Deduplicates. Runs less frequently than main loop. |
| `reap_spawns()` | `reapLoop` | `time.Ticker` (10s) | `reapTick`, `spawnRegistry` (read) | `reapDone` → `dispatchLoop` | Checks completion markers, PID liveness, stale kills. Heavy I/O — own goroutine. |
| `reclaim_stale_claimed()` | `reclaimLoop` | `time.Ticker` (60s) | `reclaimTick` | `reclaimDone` → `dispatchLoop` | Resets zombie CLAIMED tasks. Low frequency, batch-oriented. |
| `count_active()` | `slotCounter` | Triggered by `dispatchLoop` on demand | `slotQuery` | `slotResp` | Channel-served counter. Reads in-memory spawn registry, NOT MongoDB. |
| `process_pending()` | `claimLoop` | `dispatchLoop` sends available slots | `claimSlots` (int), `taskQueue` (Mongo cursor) | `claimDone` → `dispatchLoop` | Claims PENDING requests atomically (findOneAndUpdate). Builds prompt. |
| `spawn_subagent()` / `spawn_claude_code_subagent()` | `spawnSupervisor` | `claimLoop` sends `SpawnRequest` | `spawnReq` | `spawnDone` → `dispatchLoop`, `spawnRegistry` (write) | Forks `otoxan-worker` via MCP-over-stdio. Records PID in registry. |
| `record_spawn()` | Inline in `spawnSupervisor` | Called immediately after successful fork | — | Writes to `spawnRegistry` | Mutex-guarded map update. |
| `print_status()` | Inline in `dispatchLoop` | On-demand via `statusReq` | `statusReq` | `statusResp` (JSON) | Reads slotCounter + spawnRegistry. |
| `_reset_dead_task()` | `cleanupWorker` | Triggered by `reapLoop` or `count_active` | `cleanupTask` (taskID + reason) | `cleanupDone` | Idempotent reset: task→QUEUED, request→PENDING, spawn→FAILED. |
| `load_flow_for_task()` | `flowLoader` | Called from `claimLoop` before spawn | `flowLoadReq` (taskID) | `flowLoadResp` (FlowSession or fallback) | Synchronous helper, NOT a goroutine. May become goroutine if FlowStore queries are slow. |
| `build_flow_parent_prompt()` | Inline in `claimLoop` | Called after flow loaded | — | Returns prompt string | Pure function — no goroutine needed. |
| `resolve_team_routing()` | Inline in `claimLoop` | Called before spawn | — | Returns `TeamRoutingResult` | Synchronous TeamStore lookup. |
| `_is_process_alive()` | `pidWatcher` | Called from `reapLoop` | `pidCheckReq` | `pidCheckResp` (bool) | Thin wrapper around `os.FindProcess` / `syscall.Kill(0)`. Can be inline. |
| `_read_spawn_log_tail()` | Inline in `reapLoop` | Called on failure paths | — | Returns `[]string` | File I/O — stays in reaper goroutine. |
| `_resolve_workdir()` | Inline in `claimLoop` | Called before spawn | — | Returns `string` | Pure resolution logic. |
| `_get_agent_model_config()` | Inline in `claimLoop` | Called before spawn | — | Returns `(provider, model)` | YAML read — stays synchronous, cached. |
| `task_has_flow()` | Inline in `claimLoop` | Called before `load_flow_for_task` | — | Returns `bool` | Field check + string search. |

---

## 3. Shared State & Synchronization

### 3.1 In-Memory Spawn Registry

```go
type SpawnRegistry struct {
    mu     sync.RWMutex
    spawns map[string]*SpawnRecord  // key: task_id
    active int                       // cached count for O(1) slot math
}
```

- **Writers:** `spawnSupervisor` (add), `reapLoop` (update status → COMPLETED/FAILED), `cleanupWorker` (reset)
- **Readers:** `slotCounter` (count active), `reapLoop` (iterate all), `dispatchLoop` (status)
- **Sync:** `sync.RWMutex`. `slotCounter` takes `RLock` for reads; writers take `Lock`.

### 3.2 Slot Counter

```go
type SlotCounter struct {
    mu           sync.Mutex
    concurrency  int
    active       int  // RUNNING spawns with live PIDs
    claimed      int  // CLAIMED tasks (transient, not yet spawned)
}

func (sc *SlotCounter) Available() int { ... }
```

- **Updated by:** `reapLoop` (decrement on completion), `spawnSupervisor` (increment on spawn), `reclaimLoop` (decrement claimed on reset)
- **Read by:** `dispatchLoop` (compute slots for `claimLoop`)
- **Sync:** `sync.Mutex`. Updates are batched — `reapLoop` sends a delta, `dispatchLoop` applies it.

### 3.3 Dispatch Request Cache (optional optimization)

```go
type RequestCache struct {
    mu       sync.RWMutex
    pending  []DispatchRequest  // sorted by created_at
    taskIDs  map[string]bool    // dedup set
}
```

- **Why:** Avoids re-querying MongoDB every 5s when queue is empty.
- **Invalidated by:** `ensureLoop` (new requests), `claimLoop` (claims), `cleanupWorker` (resets)

---

## 4. Channel Topology Diagram

```
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
│ensureLoop│         │reapLoop  │          │reclaim   │
│(30s tick)│         │(10s tick)│          │Loop(60s) │
└────┬─────┘         └────┬─────┘          └────┬─────┘
     │ ensureDone          │ reapDone            │ reclaimDone
     └─────────────────────┼─────────────────────┘
                           ▼
                    ┌─────────────┐
                    │ dispatchLoop│ ◄── statusReq (HTTP / CLI)
                    │ (orchestrator│     statusResp (JSON)
                    │  5s ticker) │
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
        ┌─────────┐ ┌──────────┐ ┌──────────┐
        │slotQuery│ │claimSlots│ │ shutdown │
        │slotResp │ │claimDone │ │ (signal) │
        └────┬────┘ └────┬─────┘ └────┬─────┘
             │           │            │
             ▼           ▼            ▼
        ┌─────────┐ ┌──────────┐ ┌──────────┐
        │slotCounter│ │claimLoop │ │ graceful │
        │           │ │          │ │  stop    │
        └───────────┘ └────┬─────┘ └──────────┘
                           │
                           │ SpawnRequest
                           ▼
                    ┌─────────────┐
                    │spawnSupervisor│
                    │             │
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
        ┌─────────┐ ┌──────────┐ ┌──────────┐
        │spawnDone │ │spawnRegistry│ │ MCP stdio │
        │(to loop) │ │(in-mem)   │ │(to worker)│
        └─────────┘ └──────────┘ └──────────┘
                           │
                           ▼
                    ┌─────────────┐
                    │ otoxan-worker│
                    │ (subprocess) │
                    └─────────────┘
                           │
                           │ completion marker
                           ▼
                    ┌─────────────┐
                    │ /tmp/...    │
                    │ .json       │
                    └─────────────┘
```

---

## 5. Goroutine Lifecycle

### 5.1 Startup Sequence

1. **Config load** (single-shot): Read `config.yaml`, set `concurrency`, agent identity, MCP endpoints.
2. **Mongo connect** (single-shot): Authenticated connection to tasks DB.
3. **Spawn registry init** (single-shot): Hydrate from `dispatch_spawns` collection — load all `status: RUNNING` records into memory.
4. **Goroutine spawn** (parallel):
   - `dispatchLoop` starts first (blocks on channel setup)
   - `ensureLoop`, `reapLoop`, `reclaimLoop` start concurrently
   - `slotCounter`, `claimLoop`, `spawnSupervisor` start on demand (lazy)
5. **First tick**: `reapLoop` runs immediately to clean up any stale state from previous crash.

### 5.2 Shutdown Sequence

1. **SIGTERM/SIGINT** caught by `dispatchLoop`.
2. **Cancel context** → all tickers stop.
3. **Drain channels**: Wait for in-flight `claimLoop` and `spawnSupervisor` to finish.
4. **Final reap**: One last `reapLoop` pass to record completions.
5. **Flush spawn registry** to MongoDB (optional — spawns are rehydrated on next start anyway).
6. **Exit**.

---

## 6. Edge Cases & Synchronization Strategy

| Edge Case | Python Handling | Go Handling |
|-----------|----------------|-------------|
| Double-claim race | `find_one_and_update` atomic in MongoDB | Same — `findOneAndUpdate` in `claimLoop` is the single source of truth. In-memory cache is advisory. |
| Spawn dies immediately | 2s sleep + `os.kill(pid, 0)` check | `spawnSupervisor` spawns, waits 2s via `time.After`, sends `spawnDone` with `error: "spawn_died_immediately"`. `dispatchLoop` routes to `cleanupWorker`. |
| Orphan RUNNING tasks (no spawn record) | `reap_spawns()` checks running_tasks − spawned_task_ids | `reapLoop` on startup hydrates registry from DB, then scans for discrepancies. |
| Stale RUNNING (>30min) | `os.kill(SIGTERM)` + reset task | `reapLoop` compares `started_at` against `RUNNING_STALE_MINUTES`. Sends `cleanupTask`. |
| Stale CLAIMED (>10min) | `reclaim_stale_claimed()` resets | `reclaimLoop` queries `claimed_at < cutoff`, sends `cleanupTask` for each. |
| Duplicate PENDING requests | Aggregation pipeline keeps oldest | `ensureLoop` uses MongoDB `findOneAndUpdate` with upsert — idempotent. |
| Phase parents in RUNNING | Not handled in Python `count_active()` | `slotCounter` excludes `labels: ["phase-parent"]` from active count. |
| Flow path tasks | `load_flow_for_task()` + `build_flow_parent_prompt()` | `claimLoop` calls `flowLoader` (synchronous or channel-based). If fallback, proceeds with flat dispatch. |
| CC lane (Claude Code) | `spawn_claude_code_subagent()` | `spawnSupervisor` selects lane based on `parent_provider`. CC lane uses different wrapper binary. |
| Dispatcher crash mid-spawn | Wrapper guarantees completion marker | Same — `otoxan-worker` wrapper writes marker even on panic. `reapLoop` picks it up on restart. |
| Concurrency limit change | CLI arg `--concurrency` | `SIGHUP` reloads config, updates `slotCounter.concurrency` atomically. |

---

## 7. Data Structures (Go)

```go
// SpawnRecord — in-memory tracking, mirrors dispatch_spawns collection
type SpawnRecord struct {
    TaskID         string
    RequestID      string
    SessionID      string
    PID            int
    StartedAt      time.Time
    Status         SpawnStatus  // RUNNING | COMPLETED | FAILED
    ExitCode       string
    TaskStatus     string       // COMPLETED | FAILED | UNKNOWN
    LogTail        []string
    RuntimeSeconds int
    ErrorSummary   string
    Lane           string       // "hermes" | "cc"
}

// SpawnStatus — enum
type SpawnStatus int
const (
    SpawnRunning SpawnStatus = iota
    SpawnCompleted
    SpawnFailed
)

// DispatchRequest — mirrors dispatch_requests collection
type DispatchRequest struct {
    RequestID   string
    TaskID      string
    Status      RequestStatus  // PENDING | CLAIMED | FULFILLED | FAILED | DROPPED
    CreatedAt   time.Time
    ClaimedAt   *time.Time
    FulfilledAt *time.Time
    Priority    int
    Error       string
}

// RequestStatus — enum
type RequestStatus int
const (
    RequestPending RequestStatus = iota
    RequestClaimed
    RequestFulfilled
    RequestFailed
    RequestDropped
)

// SpawnRequest — sent from claimLoop to spawnSupervisor
type SpawnRequest struct {
    TaskID       string
    RequestID    string
    SessionID    string
    Prompt       string
    Toolsets     []string
    AgentName    string
    Lane         string
    Workdir      string
    FlowSession  *FlowSessionInfo  // nil for flat dispatch
}

// SpawnResult — sent from spawnSupervisor to dispatchLoop
type SpawnResult struct {
    TaskID     string
    RequestID  string
    SessionID  string
    PID        int
    LogFile    string
    Error      string  // empty if success
    SpawnedAt  time.Time
}

// ReapSummary — sent from reapLoop to dispatchLoop
type ReapSummary struct {
    ReapedCompleted  int
    ReapedFailed     int
    ReapedStale      int
    StillRunning     int
    OrphanTasksReset int
}

// ClaimSummary — sent from claimLoop to dispatchLoop
type ClaimSummary struct {
    Dispatched []DispatchInfo
    Failed     []string  // taskIDs that failed to claim
}

// DispatchInfo — what gets printed to stdout (JSON)
type DispatchInfo struct {
    Action        string          `json:"action"`
    TaskID        string          `json:"task_id"`
    RequestID     string          `json:"request_id"`
    SessionID     string          `json:"session_id"`
    PID           int             `json:"pid"`
    LogFile       string          `json:"log_file"`
    Toolsets      []string        `json:"toolsets"`
    Role          string          `json:"role"`
    PlanID        string          `json:"plan_id"`
    Title         string          `json:"title"`
    SpawnedAt     time.Time       `json:"spawned_at"`
    Agent         string          `json:"agent"`
    TeamID        string          `json:"team_id,omitempty"`
    InitiativeID  string          `json:"initiative_id,omitempty"`
    UseFlowPath   bool            `json:"use_flow_path"`
    FlowSession   *FlowSessionInfo `json:"flow_session,omitempty"`
}
```

---

## 8. MCP Worker Handoff Protocol

The `otoxan-worker` binary replaces `dispatch_spawn_wrapper.sh` + `hermes chat`. It speaks MCP-over-stdio with the dispatcher.

### 8.1 Worker Lifecycle

```
spawnSupervisor ──► fork/exec otoxan-worker ──► MCP initialize
     │                                          │
     │◄──────── run_session (task_id, prompt) ───┘
     │                                          │
     │◄──────── progress notifications ──────────┘
     │                                          │
     │◄──────── completion / failure ──────────┘
     │                                          │
     └──► reapLoop reads marker ──► MongoDB update
```

### 8.2 MCP Methods (worker → dispatcher)

| Method | Params | Direction | Purpose |
|--------|--------|-----------|---------|
| `initialize` | `agent_identity`, `mcp_endpoints` | Worker → Dispatcher | Handshake on startup. |
| `run_session` | `task_id`, `prompt`, `mcp_endpoints`, `agent_identity` | Dispatcher → Worker | Start task execution. |
| `report_progress` | `task_id`, `step`, `total_steps`, `message` | Worker → Dispatcher | Optional streaming progress. |
| `report_completion` | `task_id`, `status`, `output`, `artifacts` | Worker → Dispatcher | Terminal state. Writes completion marker. |
| `report_error` | `task_id`, `error`, `error_summary`, `last_log_lines` | Worker → Dispatcher | Terminal failure. Writes completion marker. |

### 8.3 Completion Marker Format (JSON, `/tmp/dispatch_completed/{task_id}.json`)

```json
{
  "task_id": "t_abc123",
  "task_status": "COMPLETED",
  "output": "Summary of work done...",
  "exit_code": 0,
  "runtime_seconds": 145,
  "error_summary": "",
  "last_log_lines": ["line1", "line2"],
  "session_id": "dispatch_dr_xyz_abc123",
  "completed_at": "2026-05-07T21:00:00Z"
}
```

---

## 9. Open Questions

1. **Change streams vs polling:** MongoDB is standalone (no oplog). Polling remains. Can we use a capped collection + tailable cursor for dispatch_requests to reduce poll load?
2. **Flow step advancement:** The Python flow path loads the flow template once at dispatch time. Step advancement happens in the subagent. Does the Go dispatcher need to track flow step progress, or is it fully delegated?
3. **CC lane wrapper:** `claude_dispatch_spawn_wrapper.sh` is a bash script. The Go `spawnSupervisor` can fork it directly, or we port `claude_dispatch.py` to Go as a worker variant. Which path?
4. **Per-agent systemd units:** One `otoxan-dispatch@<agent>.service` per agent, or a single dispatcher that routes by agent? The plan says per-agent — confirm.

---

## 10. File References

- `~/.hermes/scripts/dispatch.py` — Source of truth (1808 lines)
- `~/.hermes/scripts/taskqueue.py` — `TaskQueue`, `TaskExecutor`, `TaskStatus`
- `~/.hermes/scripts/flowstore.py` — `FlowStore`, `FlowSession`
- `~/.hermes/scripts/teamstore.py` — `TeamStore`, `TeamRoutingResult`
- `~/.hermes/scripts/dispatch_spawn_wrapper.sh` — Hermes lane wrapper
- `~/.hermes/scripts/claude_dispatch_spawn_wrapper.sh` — CC lane wrapper
- `~/.hermes/scripts/dispatch_complete.py` — Completion marker writer
- `~/.hermes/skills/task-queue/references/dispatch-loop-design.md` — Design rationale
