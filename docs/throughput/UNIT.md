# What is "One Otoxan Session"?

Reference: UNIT-1  
Scope: otoxan platform — all binaries, processes, and state stores

---

## 1. Definition

An **otoxan session** is the complete set of processes and state that collaborate to execute a single dispatched task from queue claim through LLM execution to completion marker. It is the atomic unit of work in the otoxan dispatch pipeline.

---

## 2. Processes Constituting a Single Session

| Process | Binary | Role | Count per Session |
|---------|--------|------|-------------------|
| **otoxan-worker** | `cmd/otoxan-worker` | Receives task prompt, calls LLM provider, writes completion marker | **1:1** — one worker process per session |
| **LLM provider client** | `internal/llm/` (Claude, OpenRouter, Mock) | HTTP client inside the worker that talks to the external model API | **1:1** — embedded in the worker |
| **MCP servers** | `cmd/otoxan-mcp-*` (tasks, plans, flows, memory, knowledge) | Standalone stdio servers that expose CRUD tools; the worker may connect to them | **Shared** — long-lived systemd services, reused across all sessions |
| **otoxan-dispatch** | `cmd/otoxan-dispatch` (goroutines in `internal/dispatch/`) | Orchestrates claim → spawn → reap; spawns the worker | **Shared** — one dispatch daemon per agent, manages many concurrent sessions |
| **otoxan CLI** | `cmd/otoxan` | Human-facing CLI; not part of the automated session lifecycle | **N/A** — used for manual task creation/inspection |

**Summary:** A single session = 1 worker process + its embedded LLM client. MCP servers and the dispatch daemon are shared infrastructure.

---

## 3. 1:1 vs Shared Components

### 3.1 1:1 (per-session)

- **otoxan-worker process** — forked by `spawnSupervisor` for each task. PID is recorded in `dispatch_spawns`. The worker lives for the duration of the task, then exits after writing its completion marker.
- **LLM HTTP client** — instantiated inside the worker (`llm.New(provider, model)`). Holds the API key, model string, and `http.Client` with timeout. Dies with the worker.
- **Completion marker file** — `/tmp/otoxan_completed/{task_id}.json`. Written once by the worker, read once by `reapLoop`, then deleted.

### 3.2 Shared (across sessions)

- **MCP servers** — `otoxan-mcp-tasks`, `otoxan-mcp-plans`, `otoxan-mcp-flows`, `otoxan-mcp-memory`, `otoxan-mcp-knowledge`. Each is a standalone binary launched via systemd (or by the CLI `otoxan mcp run <name>`). They speak JSON-RPC over stdio and connect to MongoDB. They are **stateless** — all state is in MongoDB.
- **Dispatch daemon** — `otoxan-dispatch` (or the `dispatch` subcommand). Runs long-lived goroutines (`claimLoop`, `spawnSupervisor`, `reapLoop`, `reclaimLoop`, `RunCompletionWatcher`). The in-memory `SpawnRegistry` and `SlotCounter` track all active sessions.
- **MongoDB** — Single database instance (`otoxan` or `silas`). Collections `tasks`, `plans`, `dispatch_requests`, `dispatch_spawns`, `flow_sessions`, etc. are shared by all sessions.

---

## 4. State: Where It Lives

| State | Location | Scope | Persistence |
|-------|----------|-------|-------------|
| Task document (title, status, assignee, output) | MongoDB `tasks` collection | Shared | Durable |
| Dispatch request (PENDING → CLAIMED → FULFILLED) | MongoDB `dispatch_requests` collection | Per-task | Durable |
| Spawn record (PID, started_at, status, exit_code) | MongoDB `dispatch_spawns` collection | Per-session | Durable |
| Completion marker (output, runtime, tokens) | `/tmp/otoxan_completed/{task_id}.json` | Per-session | Ephemeral (deleted after reap) |
| Flow session (step tracking, parent/child) | MongoDB `flow_sessions` collection | Shared (per flow) | Durable |
| Active spawn count, claimed slots | In-process Go (`SpawnRegistry`, `SlotCounter`) | Shared (per dispatch daemon) | Ephemeral (rehydrated from DB on restart) |
| Agent config (provider, model, concurrency) | `config.yaml` + env vars (`OTOXAN_*`) | Shared | Durable (file) |
| LLM API keys | Env vars / Infisical | Shared | Durable (external secret store) |
| MCP server tool schemas | In-memory inside each MCP server process | Shared | Ephemeral (reloaded on server restart) |

---

## 5. Lifecycle: Spawn → Idle → Active → Teardown

```
┌─────────────┐     claimLoop      ┌─────────────┐     spawnSupervisor     ┌─────────────┐
│   QUEUED    │ ─────────────────► │   CLAIMED   │ ─────────────────────► │   RUNNING   │
│  (MongoDB)  │   findOneAndUpdate │  (MongoDB)  │   fork otoxan-worker   │  (MongoDB +  │
│             │                    │             │   record PID           │   in-mem)   │
└─────────────┘                    └─────────────┘                        └──────┬──────┘
                                                                               │
                                                                               │ worker runs
                                                                               │ provider.RunSession()
                                                                               │ writes marker
                                                                               ▼
                                                                      ┌─────────────┐
                                                                      │  COMPLETED  │
                                                                      │  (marker    │
                                                                      │   file)     │
                                                                      └──────┬──────┘
                                                                             │
                                                                             │ reapLoop
                                                                             │ reads marker
                                                                             │ updates MongoDB
                                                                             ▼
                                                                      ┌─────────────┐
                                                                      │   FULFILLED │
                                                                      │   (MongoDB) │
                                                                      │  or FAILED  │
                                                                      └─────────────┘
```

### 5.1 Phase Details

| Phase | Trigger | What Happens | State Changes |
|-------|---------|--------------|---------------|
| **Spawn** | `claimLoop` finds PENDING request with free slot | `spawnSupervisor` forks `otoxan-worker` via `exec.CommandContext`. Request atomically updated to CLAIMED. Spawn record inserted with PID. | `dispatch_requests.status` → CLAIMED; `dispatch_spawns` row created |
| **Idle** | Worker process started but not yet executing | Worker parses flags, loads config, initializes LLM provider. If `--prompt` is empty, reads from stdin. | None — brief transient state |
| **Active** | Worker calls `provider.RunSession(ctx, prompt)` | HTTP request to Anthropic/OpenRouter. Streaming or single-turn response. Worker accumulates output. | Worker memory holds prompt + partial output. External API holds conversation context (if any). |
| **Teardown** | LLM call returns (success or error) | Deferred `writeCompletionMarker()` writes `/tmp/otoxan_completed/{task_id}.json`. Worker process exits. `reapLoop` picks up marker, updates `dispatch_spawns` → COMPLETED/FAILED, updates `dispatch_requests` → FULFILLED/FAILED, deletes marker. | `dispatch_spawns.status` → COMPLETED/FAILED; `dispatch_requests.status` → FULFILLED/FAILED; marker file deleted; task document updated with output |

### 5.2 Failure Paths

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Worker binary missing | `exec.LookPath` fails in `spawnOne` | Reset request to PENDING; will retry next tick |
| Worker dies immediately | `cmd.Start()` succeeds but process exits before marker | `reapLoop` sees no marker + PID dead; `cleanupWorker` resets task → QUEUED |
| Stale CLAIMED (>10 min) | `reclaimLoop` scans `claimed_at < cutoff` | Batch-reset to PENDING |
| Stale RUNNING (>30 min) | `reapLoop` compares `started_at` against threshold | Sends SIGTERM, updates spawn → FAILED, resets task |
| LLM API error (5xx) | `retryWithBackoff` in provider | Retries 3× with exponential backoff; final failure writes FAILED marker |
| LLM API error (4xx) | Provider returns non-retryable error | Immediate FAILED marker |
| Dispatch daemon crash | Process exits | On restart, `reapLoop` hydrates RUNNING spawns from DB, rescans markers, reconciles orphans |

---

## 6. ASCII Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         SHARED INFRASTRUCTURE                                │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────┐│
│  │  MongoDB    │  │  dispatch   │  │   MCP       │  │   systemd / CLI     ││
│  │  (tasks,    │  │  daemon     │  │   servers   │  │   (launchers)       ││
│  │   requests, │  │  (long-lived│  │  (long-lived│  │                     ││
│  │   spawns)   │  │   goroutines)│  │   stdio procs)│  │                     ││
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘  └─────────────────────┘│
│         │                │                │                                 │
│         │ read/write     │ fork           │ connect (optional)              │
│         │                │                │                                 │
└─────────┼────────────────┼────────────────┼─────────────────────────────────┘
          │                │                │
          │                ▼                │
          │         ┌─────────────┐         │
          │         │ otoxan-worker│◄────────┘  (MCP tools for CRUD)
          │         │ (1 per task) │
          │         └──────┬──────┘
          │                │
          │                ▼
          │         ┌─────────────┐
          │         │ LLM Provider│
          │         │ (HTTP client│
          │         │  inside worker)
          │         └──────┬──────┘
          │                │
          │                ▼
          │         ┌─────────────┐
          │         │  Anthropic  │
          │         │  /OpenRouter │
          │         │  / Mock     │
          │         └─────────────┘
          │
          │         ┌─────────────────────────┐
          └────────►│ /tmp/otoxan_completed/  │
                    │   {task_id}.json          │
                    │   (ephemeral marker)      │
                    └─────────────────────────┘
```

---

## 7. Mermaid Diagram

```mermaid
flowchart TB
    subgraph Shared["Shared Infrastructure (long-lived)"]
        DB[(MongoDB<br/>tasks, requests, spawns)]
        DD[otoxan-dispatch<br/>claimLoop | spawnSupervisor | reapLoop]
        MCP1[otoxan-mcp-tasks]
        MCP2[otoxan-mcp-plans]
        MCP3[otoxan-mcp-flows]
        MCP4[otoxan-mcp-memory]
        MCP5[otoxan-mcp-knowledge]
    end

    subgraph Session["One Otoxan Session (per task)"]
        direction TB
        W[otoxan-worker<br/>process]
        LLM[LLM Provider<br/>claude / openrouter / mock]
        M[Completion Marker<br/>/tmp/otoxan_completed/{task_id}.json]
    end

    DB -->|read PENDING| DD
    DD -->|fork + record PID| W
    W -->|optional MCP stdio| MCP1
    W -->|HTTP API call| LLM
    W -->|write| M
    DD -->|read + delete| M
    DD -->|update status| DB
```

---

## 8. Quick Reference: "Is X part of one session?"

| Component | Per-Session? | Notes |
|-----------|--------------|-------|
| `otoxan-worker` process | **Yes** | One fork per task |
| `otoxan-dispatch` goroutines | No | Shared across all tasks |
| `otoxan-mcp-*` server | No | Reused by many workers |
| MongoDB connection | No | Shared pool (foundation) |
| LLM HTTP client / API key | **Yes** | Lives inside worker |
| Completion marker file | **Yes** | One per task, ephemeral |
| `config.yaml` | No | Shared, read by all |
| Flow session state | No | Shared across flow steps |
| Task document in DB | **Yes** | One row per task, durable |
| Dispatch request row | **Yes** | One row per dispatch attempt |
| Spawn record | **Yes** | One row per worker fork |

---

## 9. File References

- `cmd/otoxan-worker/main.go` — Worker entrypoint, provider init, completion marker
- `internal/dispatch/spawn.go` — `RunSpawnSupervisor`, `spawnOne`
- `internal/dispatch/claim.go` — `RunClaimLoop`, `claimOne`
- `internal/dispatch/reap.go` — `RunReapLoop`, marker scanning
- `internal/dispatch/complete.go` — `RunCompletionWatcher`, final DB updates
- `internal/dispatch/reclaim.go` — `reclaimStale`, stale CLAIMED reset
- `internal/llm/provider.go` — `Provider` interface, `SessionResult`
- `internal/llm/claude.go` — Anthropic HTTP client implementation
- `docs/dispatch-topology.md` — Full goroutine/channel mapping
- `docs/architecture.md` — System component diagram
