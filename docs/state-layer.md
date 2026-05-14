# State Layer Reference

> Canonical reference for the otoxan MongoDB state layer.  
> Every durable thing — agents, plans, tasks, sessions, events, dispatch state, memory pointers — is read and written exclusively through this layer. No `*.json`, `*.db`, `state.sqlite`, or per-agent state files remain under `~/.otoxan/`.

---

## Table of Contents

1. [DS-1 — Database Naming](#ds-1--database-naming)
2. [DS-3 — Module Layout](#ds-3--module-layout)
3. [DS-4 — Index Specifications](#ds-4--index-specifications)
4. [DS-Hermes-parity — Public Method Audit](#ds-hermes-parity--public-method-audit)

---

## DS-1 — Database Naming

### Per-agent isolated databases

Each agent gets its own MongoDB database.  The name is derived from the agent identifier via `internal/state/resolver.go`:

```go
// AgentDB("xander") → database "otoxan_agent_xander"
func AgentDB(client *mongo.Client, name string) (*mongo.Database, error)
```

Validation rules (`ValidateAgentName`):
- Non-empty, no whitespace-only
- Lowercase letters, digits, and hyphens only (`^[a-z0-9-]+$`)
- Rejected: uppercase, slashes, underscores, spaces

### Global cross-agent database

```go
// GlobalDB returns "otoxan_global"
func GlobalDB(client *mongo.Client) *mongo.Database
```

Holds: team registry, directives, initiatives, dispatch lanes, identity manifests, auth sessions, notifications — anything that names *all* agents collectively.

### Connection string

Only the MongoDB URI lives in config (`otoxan.toml`).  All DB/collection names derive from agent name or are hard-coded constants above.  No per-store config sprawl.

---

## DS-3 — Module Layout

### Package structure

```
internal/state/
    client.go          // Singleton MongoDB client (OpenClient, ResetClient)
    client_test.go
    ping.go            // Standalone connectivity probe (Ping)
    ping_test.go
    resolver.go        // AgentDB, GlobalDB, ValidateAgentName
    resolver_test.go

internal/store/<domain>/
    model.go           // BSON structs, status enums, defaults
    store.go           // New*Store constructor + CRUD + indexes + List
    store_test.go      // Unit tests (in-memory or testcontainers)
    parity_test.go     // Go write → Python read, Python write → Go read
```

### Store domains

| Domain | Package | Collection(s) | Database |
|--------|---------|---------------|----------|
| Tasks | `internal/store/tasks` | `tasks` | per-agent |
| Plans | `internal/store/plans` | `plans` | per-agent |
| Directives | `internal/store/directives` | `directives` | per-agent |
| Reports | `internal/store/reports` | `reports` | per-agent |
| Teams | `internal/store/teams` | `teams` | `otoxan_global` |
| Flows | `internal/store/flows` | `flows` | per-agent |
| Memory | `internal/store/memory` | `memory` (metadata) + Qdrant (vectors) | per-agent |
| Notifications | `internal/store/notifications` | `notifications` | `otoxan_global` |
| Queue | `internal/store/queue` | `tasks` (via TaskStore), `task_events`, `task_counters` | per-agent |

### Soft-delete wrapper

All stores use `internal/softdelete.SoftDeleteCollection`:
- `deleted` (bool) — set `true` on soft-delete, `false` on restore
- `deleted_at` (datetime) — set on soft-delete, unset on restore
- All `Find` / `CountDocuments` auto-filter `{deleted: {$ne: true}}`
- `HardDelete` bypasses the wrapper and calls native MongoDB delete
- `WithIncludeDeleted()` option overrides the filter for admin/audit reads

---

## DS-4 — Index Specifications

Indexes are created at store construction time (`New*Store`).  No retroactive index migrations.

### Tasks (`internal/store/tasks`)

| Index | Type | Notes |
|-------|------|-------|
| `task_id` | unique | Primary lookup |
| `status` | single | Queue filtering |
| `plan_id` | single | Plan linkage |
| `epic_id` | single | Epic linkage |
| `assignee` | single | Assignment queries |
| `assignee_type` + `assignee_id` | compound | Typed assignment |
| `priority` | single | Queue ordering |
| `created_at` | single | Temporal queries |
| `status` + `priority` + `created_at` | compound | Queue cursor order |
| `scheduled_for` | sparse | Scheduled tasks only |
| `initiative_id` | sparse | Initiative-linked tasks only |

### Plans (`internal/store/plans`)

| Index | Type | Notes |
|-------|------|-------|
| `plan_id` | unique | Primary lookup |
| `status` | single | Lifecycle filtering |
| `updated_at` | single | Recency queries |
| `archived_at` | sparse | Archived plans only |
| `initiative_id` | sparse | Initiative linkage |
| `directive_id` | sparse | Directive linkage |
| `team_id` | sparse | Team linkage |
| `flow_session_id` | sparse | Flow linkage |

### Directives (`internal/store/directives`)

| Index | Type | Notes |
|-------|------|-------|
| `directive_id` | unique | Primary lookup |
| `category` | single | Category filtering |
| `priority` | single | Priority ordering |
| `enabled` | single | Active directives |
| `updated_at` | single | Recency queries |

### Reports (`internal/store/reports`)

| Index | Type | Notes |
|-------|------|-------|
| `report_id` | unique | Primary lookup |
| `status` | single | Lifecycle filtering |
| `updated_at` | single | Recency queries |
| `archived_at` | sparse | Archived reports only |

### Teams (`internal/store/teams`)

| Index | Type | Notes |
|-------|------|-------|
| `team_id` | unique | Primary lookup |
| `status` | single | Lifecycle filtering |

### Flows (`internal/store/flows`)

| Index | Type | Notes |
|-------|------|-------|
| `flow_id` | unique | Primary lookup |
| `status` | single | Lifecycle filtering |
| `updated_at` | single | Recency queries |
| `initiative_id` | sparse | Initiative linkage |
| `team_id` | sparse | Team linkage |
| `session_id` | sparse | Session linkage |

### Memory (`internal/store/memory`)

| Index | Type | Notes |
|-------|------|-------|
| `memory_id` | unique | Primary lookup |
| `agent_id` | single | Per-agent queries |
| `session_id` | sparse | Session-scoped memories |
| `type` | single | Type filtering |
| `created_at` | single | Temporal queries |
| `vector_id` | sparse | Qdrant linkage |

### Notifications (`internal/store/notifications`)

| Index | Type | Notes |
|-------|------|-------|
| `notification_id` | unique | Primary lookup |
| `recipient_id` | single | Per-recipient queries |
| `channel` | single | Channel filtering |
| `status` | single | Lifecycle filtering |
| `created_at` | single | Temporal queries |
| `sent_at` | sparse | Sent notifications only |

### Queue events (`internal/store/queue`)

| Index | Type | Notes |
|-------|------|-------|
| `task_id` + `sequence` | unique | Event ordering |
| `task_id` | single | Per-task events |
| `timestamp` | single | Temporal queries |

---

## DS-Hermes-parity — Public Method Audit

Each store exposes a public API that mirrors the corresponding Python `*Store` / `*Queue` class.  The parity tests (`parity_test.go` in every store package) verify:

1. **Go write → Python read** — Go `Create` inserts a document; Python reads it back via `testutil.PythonReadFixture`
2. **Python write → Go read** — Python inserts a fixture; Go `Get` reads it back
3. **Soft-delete round-trip** — Go `Delete` soft-deletes; Python sees `deleted=true`; Go `Restore` brings it back

### Standard CRUD surface (all stores)

```go
func New*Store(coll *mongo.Collection) *Store
func (s *Store) Create(ctx context.Context, doc *Model) (*mongo.InsertOneResult, error)
func (s *Store) Get(ctx context.Context, id string) (*Model, error)
func (s *Store) GetWithDeleted(ctx context.Context, id string) (*Model, error)
func (s *Store) Update(ctx context.Context, id string, updates bson.M) (*mongo.UpdateResult, error)
func (s *Store) Delete(ctx context.Context, id string) (*mongo.UpdateResult, error)      // soft
func (s *Store) Restore(ctx context.Context, id string) (*mongo.UpdateResult, error)
func (s *Store) HardDelete(ctx context.Context, id string) (*mongo.DeleteResult, error)
func (s *Store) List(ctx context.Context, opts ListOptions) ([]Model, error)
func (s *Store) Count(ctx context.Context, filter bson.M) (int64, error)
func (s *Store) Name() string
func (s *Store) Database() *mongo.Database
```

### Store-specific extensions

| Store | Extra methods | Rationale |
|-------|---------------|-----------|
| **Plans** | `Archive`, `Unarchive` | Plan lifecycle |
| **Reports** | `Archive`, `Unarchive`, `Publish`, `Unpublish`, `LinkPlan`, `UnlinkPlan` | Report publishing workflow |
| **Teams** | `AddMember`, `RemoveMember` | Member roster management |
| **Directives** | `Upsert` | Idempotent directive creation |
| **Notifications** | `MarkSent`, `MarkRead` | Delivery tracking |
| **Memory** | `Search(ctx, query []float32, k int)` | Qdrant vector search |
| **Queue** | `Claim`, `MarkRunning`, `MarkCompleted`, `MarkFailed`, `MarkRetried`, `ReclaimStale`, `QueueStatus`, `EmitEvent`, `ListEvents` | Dispatch state machine |

### Hermes → otoxan mapping

| Hermes Python | otoxan Go | Status |
|---------------|-----------|--------|
| `taskqueue.TaskQueue` | `internal/store/queue` + `internal/store/tasks` | ✅ Implemented |
| `planstore.PlanStore` | `internal/store/plans` | ✅ Implemented |
| `directivestore.DirectiveStore` | `internal/store/directives` | ✅ Implemented |
| `reportstore.ReportStore` | `internal/store/reports` | ✅ Implemented |
| `teamstore.TeamStore` | `internal/store/teams` | ✅ Implemented |
| `flowstore.FlowStore` | `internal/store/flows` | ✅ Implemented |
| `agent_memory.AgentMemory` | `internal/store/memory` + Qdrant | ✅ Implemented |
| `notifications.MongoNotifications` | `internal/store/notifications` | ✅ Implemented |
| `auth.MongoAuth` | `internal/auth` (users raw, sessions soft-delete) | ✅ Implemented |

### Parity test matrix

All 9 store domains have `parity_test.go` files with the standard three-test pattern:

```
internal/store/tasks/parity_test.go      → TestTaskStore_Parity_*
internal/store/plans/parity_test.go      → TestPlanStore_Parity_*
internal/store/directives/parity_test.go → TestDirectiveStore_Parity_*
internal/store/reports/parity_test.go    → TestReportStore_Parity_*
internal/store/teams/parity_test.go      → TestTeamStore_Parity_*
internal/store/flows/parity_test.go      → TestFlowStore_Parity_*
internal/store/memory/parity_test.go      → TestMemoryStore_Parity_*
internal/store/notifications/parity_test.go → TestNotificationStore_Parity_*
internal/store/queue/parity_test.go       → TestQueueStore_Parity_*
```

The `internal/testutil` package provides shared helpers:
- `PythonReadFixture(t, domain, id)` — reads a document via Python bridge
- `PythonWriteFixture(t, domain, id)` — writes a fixture via Python bridge
- `NormalizeTimeFields(t, doc)` — truncates datetime precision for cross-language comparison
- `AssertParityString`, `AssertParityInt`, `AssertParityBool` — type-safe field assertions

---

## No on-disk state

After this layer ships, the only files under `~/.otoxan/` are:
- `otoxan.toml` — connection string (usually pointing at Infisical)
- Log files (if any) — ephemeral, not durable state

All agent-scoped durable data lives in `otoxan_agent_<name>`.  All global durable data lives in `otoxan_global`.  The rest of the base plate (Xander, identity, Qdrant, docker, backup) and `init_otoxan_v1` (dispatch/MCP/etc.) build on this foundation.
