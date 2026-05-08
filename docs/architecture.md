# Architecture

Reference: DS-3

## Overview

otoxan is a Go rewrite of the legacy Hermes Python system.
It provides a CLI and daemon for managing AI agent operations:
tasks, plans, teams, flows, memory, and dispatch.
The system is built on three layers:

1. **Foundation** — `autozan-go-foundation` provides shared MongoDB connectivity,
   soft-delete wrappers, logging, and configuration primitives.
2. **Stores** — MongoDB-backed document stores with BSON shapes matching the
   Python originals.
3. **Dispatch** — A goroutine-based task dispatcher that manages slot counting,
   spawn tracking, and MCP worker lifecycle in memory.

## Component Diagram

```text
┌─────────────────────────────────────────────────────────────┐
│                         CLI (otoxan)                          │
│  init │ task │ plan │ team │ flow │ memory │ dispatch │ ...  │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                     Config (YAML + Env)                       │
│  $OTOXAN_HOME/config.yaml  +  OTOXAN_* env vars             │
└─────────────────────────────────────────────────────────────┘
                              │
              ┌───────────────┼───────────────┐
              ▼               ▼               ▼
        ┌─────────┐    ┌─────────┐    ┌─────────────┐
        │ Stores  │    │ Dispatch│    │ MCP Servers │
        │(MongoDB)│    │(Memory) │    │ (stdio)     │
        └────┬────┘    └────┬────┘    └─────┬───────┘
             │              │                 │
             └──────────────┼─────────────────┘
                            ▼
                    ┌───────────────┐
                    │   MongoDB     │
                    │  (tasks,      │
                    │   plans,      │
                    │   spawns,     │
                    │   requests)   │
                    └───────────────┘
```

## Foundation

`autozan-go-foundation` is a shared Go module providing:

- MongoDB client construction with URI resolution (env → Infisical)
- Soft-delete collection wrapper (`{deleted: {$ne: true}}` auto-filter)
- Structured logging
- Configuration primitives (env overlay, strict mode)

otoxan depends on foundation for all database access.
No store talks to MongoDB directly without the soft-delete wrapper.

## Stores

Each domain has a MongoDB-backed store in `internal/store/<domain>/`:

| Store | Collection | Key Features |
|-------|------------|--------------|
| `tasks` | `tasks` | CRUD, list filtering, soft-delete, priority + status indexes |
| `plans` | `plans` | Status lifecycle, soft-delete, tags, initiative linkage |
| `teams` | `teams` | Member roster, role management |
| `flows` | `flow_sessions` | Session state, parent/child relationships |
| `memory` | `memory` | Vector search (planned), temporal context |
| `knowledge` | `knowledge` | Document chunks, embeddings |

All stores use the same BSON field names as the Python originals
to ensure compatibility with existing MongoDB data.

## Dispatch

The dispatch daemon (`cmd/otoxan-dispatch`, in progress) replaces the Python
`dispatch.py` polling loop with a set of long-lived goroutines:

| Goroutine | Responsibility | Frequency |
|-----------|---------------|-----------|
| `dispatchLoop` | Orchestrator; receives summaries, prints status | 5s ticker |
| `ensureLoop` | Bridges QUEUED tasks → PENDING dispatch_requests | 30s |
| `reapLoop` | Checks completion markers, PID liveness, stale kills | 10s |
| `reclaimLoop` | Resets zombie CLAIMED tasks | 60s |
| `claimLoop` | Claims PENDING requests atomically, builds prompts | On demand |
| `spawnSupervisor` | Forks `otoxan-worker` via MCP-over-stdio | On demand |

Transient coordination (slot counting, spawn tracking) lives in memory.
MongoDB is written to only for durable state transitions.

See `docs/dispatch-topology.md` for the full goroutine/channel mapping.

## MCP Servers

MCP servers are standalone binaries that communicate over stdio using JSON-RPC 2.0
(newline-delimited). They expose tools for CRUD on each store domain.

| Binary | Domain | Tools |
|--------|--------|-------|
| `otoxan-mcp-tasks` | Tasks | create, get, list, update, delete |
| `otoxan-mcp-memory` | Memory | create, get, search, delete |
| `otoxan-mcp-knowledge` | Knowledge | create, get, search, delete |
| `otoxan-mcp-flows` | Flows | create, get, list, update, delete |

See `docs/mcp-spec-notes.md` for transport details and mandatory methods.

## Configuration

Configuration is loaded from two sources, in order of increasing precedence:

1. `$OTOXAN_HOME/config.yaml` (or `$XDG_DATA_HOME/otoxan/config.yaml`)
2. Environment variables prefixed with `OTOXAN_`

The `OTOXAN_` prefix guarantees that otoxan never reads legacy Hermes
variables (e.g. `HERMES_MONGO_URI`) and vice-versa during the shadow-mode
migration.

See `docs/configuration.md` for the full schema.

## Path Resolution

The otoxan home directory is resolved in this priority:

1. `$OTOXAN_HOME`
2. `$XDG_DATA_HOME/otoxan`
3. `~/.local/share/otoxan`

All profiles, configs, and runtime data live under this directory.
No hardcoded `/home/silas/.hermes/` paths remain in the codebase.
