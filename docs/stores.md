# Stores

Reference: DS-3, `docs/store-inventory.md`

## Overview

otoxan stores are MongoDB-backed document repositories with soft-delete semantics.
Each store lives in `internal/store/<domain>/` and mirrors the Python original's CRUD surface.

## Soft-Delete Semantics

All stores use the `softdelete` wrapper from `autozan-go-foundation`:

- `deleted` (bool) — set `true` on soft-delete, `false` on restore
- `deleted_at` (datetime) — set on soft-delete, unset on restore
- All `find` / `count` / `aggregate` auto-filter `{deleted: {$ne: true}}`
- `HardDelete` bypasses the wrapper and calls native MongoDB delete

## Task Store

**Package:** `internal/store/tasks`
**Collection:** `tasks`

### Task Model

```go
type Task struct {
    TaskID       string     `bson:"task_id"`
    Title        string     `bson:"title"`
    Description  string     `bson:"description"`
    Status       TaskStatus `bson:"status"`
    Type         TaskType   `bson:"type"`
    Priority     int        `bson:"priority"`
    Assignee     string     `bson:"assignee"`
    AssigneeType string     `bson:"assignee_type"`
    PlanID       string     `bson:"plan_id"`
    EpicID       string     `bson:"epic_id"`
    InitiativeID string     `bson:"initiative_id"`
    CreatedAt    time.Time  `bson:"created_at"`
    UpdatedAt    time.Time  `bson:"updated_at"`
    ScheduledFor *time.Time `bson:"scheduled_for,omitempty"`
    Output       string     `bson:"output"`
    MaxRetries   int        `bson:"max_retries"`
    RetryCount   int        `bson:"retry_count"`
    Deleted      bool       `bson:"deleted,omitempty"`
    DeletedAt    *time.Time `bson:"deleted_at,omitempty"`
}
```

### Status Values

| Status | Meaning |
|--------|---------|
| `QUEUED` | Waiting for dispatcher |
| `PENDING` | Dispatch request created |
| `CLAIMED` | Assigned to a worker |
| `RUNNING` | Worker executing |
| `COMPLETED` | Success |
| `FAILED` | Error, may retry |
| `BLOCKED` | Dependency not met |
| `CANCELLED` | Manually cancelled |

### CRUD Methods

```go
Create(ctx, *Task) (*mongo.InsertOneResult, error)
Get(ctx, taskID string) (*Task, error)
List(ctx, ListOptions) ([]*Task, error)
Update(ctx, taskID string, bson.M) (*mongo.UpdateResult, error)
Delete(ctx, taskID string) (*mongo.UpdateResult, error)      // soft
HardDelete(ctx, taskID string) (*mongo.DeleteResult, error)
```

### Indexes

- `task_id` (unique)
- `status`, `plan_id`, `epic_id`, `assignee`, `priority`, `created_at`
- Compound: `status + priority + created_at`
- Sparse: `scheduled_for`, `initiative_id`

## Plan Store

**Package:** `internal/store/plans`
**Collection:** `plans`

### Plan Model

See `internal/store/plans/model.go` for the full struct. Key fields:

- `plan_id` — slug identifier
- `title` — human-readable name
- `status` — `PLANNING`, `EXECUTING`, `PAUSED`, `COMPLETED`, `ABANDONED`, `CHECKING`, `ACCEPTED`, `REGRESSED`
- `content` — Markdown body
- `tags` — string array
- `initiative_id`, `directive_id`, `team_id` — optional foreign keys

### CRUD Methods

Same pattern as TaskStore: `Create`, `Get`, `List`, `Update`, `Delete`,
`HardDelete`.

## Other Stores

| Store | Status | Notes |
|-------|--------|-------|
| `teams` | Planned | Member roster, role management |
| `flows` | Planned | Session state, parent/child |
| `memory` | Planned | Vector search, temporal context |
| `knowledge` | Planned | Document chunks, embeddings |

## BSON Compatibility

All BSON field names match the Python originals exactly. This ensures that existing
MongoDB data is readable without migration. Go `bson` tags must match the Python
dictionary keys character-for-character.
