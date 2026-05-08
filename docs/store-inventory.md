# Store Inventory — Hermes → otoxan Go Port

> Research artefact: every Mongo collection field used by the 10 Hermes stores,
> with Python types, sample values, and soft-delete participation.
> Generated 2026-05-07 from live MongoDB samples + source-code inspection.

---

## Legend

| Column | Meaning |
|--------|---------|
| `Field` | BSON key (exact — Go `bson:"…"` tag must match) |
| `Python type` | Type as written in Python source |
| `Go analogue` | Suggested Go type for the port |
| `Sample` | Value from a live document (truncated) |
| `Soft-delete?` | Whether the field is mutated by `SoftDeleteCollection` |

Soft-delete semantics (from `softdelete/__init__.py`):
- `deleted` (bool) — set `True` on soft-delete, `False` on restore
- `deleted_at` (datetime) — set on soft-delete, unset on restore
- All `find` / `count` / `aggregate` auto-filter `{deleted: {$ne: true}}`
- `hard_delete_*` bypasses the wrapper and calls native PyMongo delete

---

## 1. taskstore — `TaskStore`

Database: per-agent (e.g. `silas`).  
Soft-delete wrapper: **yes** on all three collections.

### 1.1 `pipelines`

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69eff674…` | no |
| `agent` | `str` | `string` | `"silas"` | no |
| `name` | `str` | `string` | `"chart-hackers-monitor"` | no |
| `schedule` | `str` | `string` | `"0 9 * * *"` | no |
| `description` | `str` | `string` | `"Daily chart scan"` | no |

*Note: `pipelines` collection is created in `_connect()` but the CRUD methods shown in source only use `runs` and `artifacts`. The `list_pipelines()` query suggests the shape above.*

### 1.2 `runs`

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69eff674…` | no |
| `run_id` | `str` | `string` | `"550e8400-e29b-41d4-a716-446655440000"` | no |
| `pipeline_name` | `str` | `string` | `"chart-hackers-monitor"` | no |
| `agent` | `str` | `string` | `"silas"` | no |
| `status` | `str` | `string` | `"running"` / `"success"` / `"failed"` | no |
| `started_at` | `datetime` | `time.Time` | `2026-04-27 23:51:16.699000+00:00` | no |
| `finished_at` | `datetime \| None` | `*time.Time` | `null` | no |
| `trigger` | `dict` | `map[string]any` | `{"video_id": "abc123"}` | no |
| `metadata` | `dict` | `map[string]any` | `{}` | no |
| `artifact_count` | `int` | `int` | `0` | no |
| `error_message` | `str \| None` | `*string` | `null` | no |
| `deleted` | `bool` | `bool` | `false` | **yes** |
| `deleted_at` | `datetime \| None` | `*time.Time` | `null` | **yes** |

### 1.3 `artifacts`

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69eff674…` | no |
| `artifact_id` | `str` | `string` | `"550e8400-…"` | no |
| `run_id` | `str` | `string` | `"550e8400-…"` | no |
| `agent` | `str` | `string` | `"silas"` | no |
| `type` | `str` | `string` | `"trade_card"` / `"report"` | no |
| `content` | `dict` | `map[string]any` | `{"ticker": "SOL", "direction": "long"}` | no |
| `file_path` | `str \| None` | `*string` | `"reports/2026-04-23.md"` | no |
| `created_at` | `datetime` | `time.Time` | `2026-04-27 23:51:16.699000+00:00` | no |
| `deleted` | `bool` | `bool` | `false` | **yes** |
| `deleted_at` | `datetime \| None` | `*time.Time` | `null` | **yes** |

---

## 2. planstore — `PlanStore`

Database: per-agent (e.g. `silas`). Collection: `plans`.  
Soft-delete wrapper: **yes**.

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69eff674b64c8e9080327048` | no |
| `plan_id` | `str` | `string` | `"music-agent-drum-matching"` | no |
| `title` | `str` | `string` | `"Music Agent - Drum Reference Matching Engine"` | no |
| `status` | `str` | `string` | `"PLANNING"` / `"EXECUTING"` / `"COMPLETED"` … | no |
| `owner` | `str` | `string` | `"silas"` | no |
| `created_at` | `datetime` | `time.Time` | `2026-04-27 23:51:16.699000+00:00` | no |
| `updated_at` | `datetime` | `time.Time` | `2026-05-06 19:43:24.421000+00:00` | no |
| `content` | `str` | `string` | `"---\nplan_id: …\n# …"` (Markdown) | no |
| `tags` | `List[str]` | `[]string` | `["music", "audio", "backend"]` | no |
| `created_session` | `str` | `string` | `""` | no |
| `updated_sessions` | `List[str]` | `[]string` | `[]` | no |
| `plan_type` | `str` | `string` | `"standard"` | no |
| `initiative_id` | `str \| None` | `*string` | `null` | no |
| `directive_id` | `str \| None` | `*string` | `null` | no |
| `team_id` | `str \| None` | `*string` | `null` | no |
| `flow_session_id` | `str \| None` | `*string` | `null` | no |
| `entity_type` | `str \| None` | `*string` | `null` | no |
| `flow_ref` | `str \| None` | `*string` | `null` | no |
| `plan_flow_id` | `str \| None` | `*string` | `null` | no |
| `acceptance` | `dict \| None` | `map[string]any` | `null` | no |
| `failure_context` | `dict \| None` | `map[string]any` | `null` | no |
| `archived_at` | `datetime \| None` | `*time.Time` | `null` | no |
| `deleted` | `bool` | `bool` | `false` | **yes** |
| `deleted_at` | `datetime \| None` | `*time.Time` | `null` | **yes** |

*Indexes:* `plan_id` (unique), `status`, `updated_at`, `archived_at`, `initiative_id` (sparse), `directive_id` (sparse), `team_id` (sparse), `flow_session_id` (sparse).

---

## 3. directivestore — `DirectiveStore`

Database: per-agent (e.g. `silas`). Collection: `directives`.  
Soft-delete wrapper: **yes**.

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69f957db202c23ae2418038b` | no |
| `directive_id` | `str` | `string` | `"no-openrouter"` | no |
| `title` | `str` | `string` | `"No OpenRouter"` | no |
| `content` | `str` | `string` | `"Never propose OpenRouter…"` | no |
| `category` | `str` | `string` | `"infrastructure"` | no |
| `priority` | `int` | `int` | `100` | no |
| `enabled` | `bool` | `bool` | `true` | no |
| `tags` | `List[str]` | `[]string` | `["providers", "infrastructure", "hard-block"]` | no |
| `owner` | `str` | `string` | `"silas"` | no |
| `created_at` | `datetime` | `time.Time` | `2026-05-05 17:54:31.520000+00:00` | no |
| `updated_at` | `datetime` | `time.Time` | `2026-05-05 18:04:25.196000+00:00` | no |
| `polarity` | `str` | `string` | `"negative"` | no |
| `scope` | `str` | `string` | `"global"` | no |
| `deleted` | `bool` | `bool` | `false` | **yes** |
| `deleted_at` | `datetime \| None` | `*time.Time` | `null` | **yes** |

*Indexes:* `directive_id` (unique), `category`, `priority`, `enabled`, `updated_at`.

---

## 4. reportstore — `ReportStore`

Database: per-agent (e.g. `silas`). Collection: `reports`.  
Soft-delete wrapper: **yes**.

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69f992e6398459bbf11c0322` | no |
| `report_id` | `str` | `string` | `"test-competitive-intel"` | no |
| `title` | `str` | `string` | `"Competitive Analysis: AI Agent Market Q2 2026"` | no |
| `status` | `str` | `string` | `"DRAFT"` / `"PUBLISHED"` / `"ARCHIVED"` | no |
| `owner` | `str` | `string` | `"silas"` | no |
| `created_at` | `datetime` | `time.Time` | `2026-05-05 06:49:10.715000+00:00` | no |
| `updated_at` | `datetime` | `time.Time` | `2026-05-05 06:49:50.917000+00:00` | no |
| `content` | `str` | `string` | `"# Competitive Analysis…"` | no |
| `tags` | `List[str]` | `[]string` | `["research"]` | no |
| `linked_plan_id` | `str \| None` | `*string` | `null` | no |
| `created_session` | `str` | `string` | `""` | no |
| `updated_sessions` | `List[str]` | `[]string` | `[]` | no |
| `archived_at` | `datetime \| None` | `*time.Time` | `null` | no |
| `deleted` | `bool` | `bool` | `false` | **yes** |
| `deleted_at` | `datetime \| None` | `*time.Time` | `null` | **yes** |

*Indexes:* `report_id` (unique), `status`, `updated_at`, `archived_at`.

---

## 5. teamstore — `TeamStore`

Database: `global` (registry) + `team_{team_id}` (per-team).  
Soft-delete wrapper: **no** — uses raw PyMongo collections.

### 5.1 `global.teams`

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69fa2ed757cb127403c8dc76` | no |
| `team_id` | `str` | `string` | `"personal_brand"` | no |
| `name` | `str` | `string` | `"Matt's Personal Brand"` | no |
| `db_name` | `str` | `string` | `"team_personal_brand"` | no |
| `directive_id` | `str \| None` | `*string` | `"brand_grow"` | no |
| `members` | `List[dict]` | `[]Member` | `[{"agent":"matt","role":"owner","type":"human","joined_at":"…"}]` | no |
| `artifacts` | `dict` | `map[string]any` | `{}` | no |
| `status` | `str` | `string` | `"ACTIVE"` / `"FORMING"` / `"PAUSED"` / `"RETIRED"` | no |
| `created_at` | `datetime` | `time.Time` | `2026-05-05 17:54:31.520000+00:00` | no |
| `updated_at` | `datetime` | `time.Time` | `2026-05-05 18:04:25.196000+00:00` | no |

### 5.2 `global.agents`

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69fa2ed7a68d172ef02025f4` | no |
| `agent_id` | `str` | `string` | `"silas"` | no |
| `name` | `str` | `string` | `"Silas"` | no |
| `db_name` | `str` | `string` | `"silas"` | no |
| `profile_path` | `str` | `string` | `"/home/silas/.hermes/profiles/silas"` | no |
| `role` | `str \| None` | `*string` | `"primary"` | no |
| `status` | `str` | `string` | `"ACTIVE"` / `"INACTIVE"` / `"RETIRED"` | no |
| `created_at` | `datetime` | `time.Time` | `2026-05-05 17:54:31.520000+00:00` | no |
| `updated_at` | `datetime` | `time.Time` | `2026-05-05 18:04:25.196000+00:00` | no |
| `deleted` | `bool` | `bool` | `false` | no *(raw collection)* |
| `deleted_at` | `datetime \| None` | `*time.Time` | `null` | no |

### 5.3 `team_{id}.directives`

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69fa2ed757cb127403c8dc77` | no |
| `directive_id` | `str` | `string` | `"brand_grow"` | no |
| `team_id` | `str` | `string` | `"personal_brand"` | no |
| `statement` | `str` | `string` | `"Grow Matt's personal brand…"` | no |
| `success_criteria` | `List[dict]` | `[]Criterion` | `[{"metric":"revenue","target":"monthly_recurring","unit":"USD"}]` | no |
| `status` | `str` | `string` | `"ACTIVE"` / `"REVISED"` / `"RETIRED"` | no |
| `version` | `int` | `int` | `1` | no |
| `created_at` | `datetime` | `time.Time` | `2026-05-05 17:54:31.520000+00:00` | no |
| `updated_at` | `datetime` | `time.Time` | `2026-05-05 18:04:25.196000+00:00` | no |

*Index:* `(directive_id, version)` unique.

### 5.4 `team_{id}.initiatives`

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69fa2ed757cb127403c8dc78` | no |
| `initiative_id` | `str` | `string` | `"instagram_5k"` | no |
| `directive_id` | `str` | `string` | `"brand_grow"` | no |
| `team_id` | `str` | `string` | `"personal_brand"` | no |
| `title` | `str` | `string` | `"Grow Instagram to 5K followers"` | no |
| `description` | `str` | `string` | `"Build Instagram presence…"` | no |
| `success_criteria` | `List[dict]` | `[]Criterion` | `[{"metric":"follower_count","target":"5000","unit":"followers"}]` | no |
| `timeline` | `dict` | `Timeline` | `{"started_at":null,"target_completion":null,"completed_at":null}` | no |
| `plan_ids` | `List[str]` | `[]string` | `[]` | no |
| `status` | `str` | `string` | `"PROPOSED"` / `"ACTIVE"` / `"MEASURING"` / `"SUCCEEDED"` / `"FAILED"` / `"PIVOTED"` | no |
| `outcome_notes` | `str` | `string` | `""` | no |
| `created_at` | `datetime` | `time.Time` | `2026-05-05 17:54:31.520000+00:00` | no |
| `updated_at` | `datetime` | `time.Time` | `2026-05-05 18:04:25.196000+00:00` | no |

---

## 6. flowstore — `FlowStore`

Database: `hermes` (default) or explicit.  
Collections: `session_flows` (soft-delete **yes**), `flow_sessions` (soft-delete **no** — raw collection).

### 6.1 `session_flows` (templates)

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | — | no |
| `flow_id` | `str` | `string` | `"idea_to_plan"` | no |
| `name` | `str` | `string` | `"Idea to Plan"` | no |
| `description` | `str` | `string` | `"Capture an idea, research it, then create a structured plan."` | no |
| `version` | `int` | `int` | `1` | no |
| `builtin` | `bool` | `bool` | `true` | no |
| `scope` | `str` | `string` | `"session"` / `"task"` | no |
| `steps` | `List[dict]` | `[]FlowStep` | `[{"step_id":"capture_idea","name":"Capture Idea","type":"define",…}]` | no |
| `created_at` | `datetime` | `time.Time` | `2026-05-07 14:56:00+00:00` | no |
| `updated_at` | `datetime` | `time.Time` | `2026-05-07 14:56:00+00:00` | no |
| `deleted` | `bool` | `bool` | `false` | **yes** |
| `deleted_at` | `datetime \| None` | `*time.Time` | `null` | **yes** |

*FlowStep shape:* `step_id`, `name`, `description`, `type`, `optional`, `prompt_template`, `defaults`, `output_field`, `delegate_to`, `verify_template`.

### 6.2 `flow_sessions`

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | — | no |
| `session_id` | `str` | `string` | `"sess_a1b2c3d4e5f6"` | no |
| `flow_id` | `str` | `string` | `"idea_to_plan"` | no |
| `scope` | `str` | `string` | `"session"` | no |
| `status` | `str` | `string` | `"ACTIVE"` / `"PAUSED"` / `"COMPLETED"` / `"ABANDONED"` | no |
| `current_step` | `int` | `int` | `0` | no |
| `parent_session_id` | `str \| None` | `*string` | `null` | no |
| `outputs` | `dict` | `map[string]any` | `{}` | no |
| `errors` | `List[str]` | `[]string` | `[]` | no |
| `plan_id` | `str \| None` | `*string` | `null` | no |
| `team_id` | `str \| None` | `*string` | `null` | no |
| `directive_id` | `str \| None` | `*string` | `null` | no |
| `initiative_id` | `str \| None` | `*string` | `null` | no |
| `parent_entity` | `str \| None` | `*string` | `null` | no |
| `notes` | `str` | `string` | `""` | no |
| `created_at` | `datetime` | `time.Time` | `2026-05-07 14:56:00+00:00` | no |
| `updated_at` | `datetime` | `time.Time` | `2026-05-07 14:56:00+00:00` | no |
| `created_by` | `str` | `string` | `"silas"` | no |
| `total_steps` | `int` | `int` | `4` | no |
| `parent_task_id` | `str \| None` | `*string` | `null` | no |
| `task_refs` | `List[str]` | `[]string` | `[]` | no |

*Note:* `flow_sessions` is **not** wrapped by `SoftDeleteCollection` in the source. The `InMemoryFlowStore` fallback also has no soft-delete.

---

## 7. agent_memory — `AgentMemory`

Backend: **Qdrant** (vector DB), not MongoDB.  
Collection name pattern: `{agent}_memory` (e.g. `silas_memory`).  
Soft-delete: **yes** — implemented via Qdrant payload fields, not Mongo.

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `memory_id` | `str` (UUID) | `string` | `"abc123…"` | no |
| `content` | `str` | `string` | `"Matt prefers direct action…"` | no |
| `memory_type` | `str` | `string` | `"fact"` / `"episodic"` / `"procedural"` / `"reflection"` / `"summary"` | no |
| `importance` | `float` | `float64` | `0.9` | no |
| `source` | `str` | `string` | `"agent"` / `"user"` / `"consolidation"` / `"tool_result"` | no |
| `agent` | `str` | `string` | `"archer"` | no |
| `created_at` | `str` (ISO) | `string` | `"2026-05-07T14:56:00+00:00"` | no |
| `updated_at` | `str` (ISO) | `string` | `null` | no |
| `last_accessed` | `str` (ISO) | `string` | `"2026-05-07T14:56:00+00:00"` | no |
| `access_count` | `int` | `int` | `0` | no |
| `tags` | `List[str]` | `[]string` | `[]` | no |
| `linked_memory_ids` | `List[str]` | `[]string` | `[]` | no |
| `deleted` | `bool` | `bool` | `false` | **yes** |
| `deleted_at` | `str \| None` (ISO) | `*string` | `null` | **yes** |

*Note:* Qdrant stores vector + payload; there is no `_id` field. The `memory_id` is the Qdrant point ID.  
*Flag:* This store is **not Mongo-backed** — porting to Go requires Qdrant client (or a Mongo fallback).

---

## 8. notifications — `MongoNotifications`

Database: `global`. Collection: `notifications`.  
Soft-delete wrapper: **yes**.

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69f8fcb1dea8ae227b12821f` | no |
| `notification_id` | `str` | `string` | `"n_2b544fb"` | no |
| `user_id` | `str` | `string` | `"user_matt"` | no |
| `type` | `str` | `string` | `"task_assigned"` | no |
| `message` | `str` | `string` | `"New task assigned to you: Human approval"` | no |
| `task_id` | `str \| None` | `*string` | `"t_8772db0"` | no |
| `agent` | `str \| None` | `*string` | `"test_agent"` | no |
| `read` | `bool` | `bool` | `false` | no |
| `created_at` | `datetime` | `time.Time` | `2026-05-04 20:08:17.102000+00:00` | no |
| `read_at` | `datetime \| None` | `*time.Time` | `null` | no |
| `deleted` | `bool` | `bool` | `false` | **yes** |
| `deleted_at` | `datetime \| None` | `*time.Time` | `null` | **yes** |

*Indexes:* `user_id`, `read`, `(user_id, read)`, `created_at`.

---

## 9. auth — `MongoAuth`

Database: `global`.  
Collections: `users` (raw), `sessions` (soft-delete **yes**).

### 9.1 `users`

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69f8f29aec6a34965a4a80cf` | no |
| `email` | `str` | `string` | `"admin@hermes.local"` | no |
| `password_hash` | `str` | `string` | `"$argon2id$v=19$m=65536…"` | no |
| `name` | `str` | `string` | `"Admin"` | no |
| `roles` | `List[str]` | `[]string` | `["admin"]` | no |
| `status` | `str` | `string` | `"active"` / `"disabled"` | no |
| `created_by` | `str` | `string` | `"bootstrap"` | no |
| `created_at` | `datetime` | `time.Time` | `2026-05-04 19:25:14.037000+00:00` | no |
| `updated_at` | `datetime` | `time.Time` | `2026-05-05 21:56:49.375000+00:00` | no |
| `last_login` | `datetime \| None` | `*time.Time` | `2026-05-06 21:06:44.178000+00:00` | no |
| `discord_username` | `str \| None` | `*string` | `"test_user#1234"` | no |
| `display_name` | `str \| None` | `*string` | `"Admin"` | no |
| `slack_username` | `str \| None` | `*string` | `null` | no |

*Indexes:* `email` (unique), `roles`, `status`.

### 9.2 `sessions`

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `str` | `string` | `"6SJ69aKk1DlgaRMg5sDL58UH1Me8rse-iMCeyKZ4csc"` | no |
| `user_id` | `str` | `string` | `"69f8fa71ff4c22afc7dd8a6d"` | no |
| `created_at` | `datetime` | `time.Time` | `2026-05-07 16:26:21.820000+00:00` | no |
| `expires_at` | `datetime` | `time.Time` | `2026-05-08 04:26:21.820000+00:00` | no |
| `ip_address` | `str \| None` | `*string` | `"127.0.0.1"` | no |
| `user_agent` | `str \| None` | `*string` | `"Mozilla/5.0 …"` | no |
| `deleted` | `bool` | `bool` | `false` | **yes** |
| `deleted_at` | `datetime \| None` | `*time.Time` | `null` | **yes** |

*Indexes:* `expires_at` (TTL), `user_id`.

---

## 10. taskqueue — `TaskQueue`

Database: per-agent (e.g. `silas`).  
Collections: `tasks` (soft-delete **yes**), `task_events` (soft-delete **yes**), `task_counters` (soft-delete **yes**).

### 10.1 `tasks`

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69fbcc287cd3464c52c438b0` | no |
| `task_id` | `str` | `string` | `"t_69e062e"` | no |
| `parent_task_id` | `str \| None` | `*string` | `null` | no |
| `epic_id` | `str \| None` | `*string` | `null` | no |
| `phase` | `str \| None` | `*string` | `null` | no |
| `phase_order` | `int \| None` | `*int` | `null` | no |
| `plan_id` | `str \| None` | `*string` | `null` | no |
| `title` | `str` | `string` | `"Linkage test: build feature"` | no |
| `description` | `str` | `string` | `"Test DS-6 bidirectional linkage"` | no |
| `type` | `str` | `string` | `"internal"` / `"github_issue"` / `"codebase_onboard"` | no |
| `directive` | `str \| List[str] \| None` | `any` | `null` | no |
| `status` | `str` | `string` | `"QUEUED"` / `"CLAIMED"` / `"RUNNING"` / `"COMPLETED"` / `"FAILED"` / `"BLOCKED"` / `"CANCELLED"` / `"SKIPPED"` / `"DRAFT"` / `"VALIDATING"` | no |
| `priority` | `int` | `int` | `1` | no |
| `scheduled_for` | `datetime \| None` | `*time.Time` | `null` | no |
| `scheduled_reason` | `str` | `string` | `""` | no |
| `labels` | `List[str]` | `[]string` | `[]` | no |
| `assignee` | `str` | `string` | `"silas"` | no |
| `assignee_type` | `str` | `string` | `"agent"` / `"human"` | no |
| `assignee_id` | `str \| None` | `*string` | `null` | no |
| `attempts` | `int` | `int` | `0` | no |
| `max_retries` | `int` | `int` | `3` | no |
| `depends_on` | `List[str]` | `[]string` | `[]` | no |
| `parallel_group` | `str \| None` | `*string` | `null` | no |
| `retry_config` | `dict` | `RetryConfig` | `{"backoff":"exponential","initial_delay_seconds":30,"max_delay_seconds":300,"multiplier":2}` | no |
| `failure_pattern` | `str` | `string` | `"notify_and_halt"` | no |
| `failure_context` | `dict` | `map[string]any` | `{"notify_channel":"","include_logs":true,"include_summary":true}` | no |
| `verification` | `str \| None` | `*string` | `null` | no |
| `intent` | `str` | `string` | `""` | no |
| `implementation` | `str` | `string` | `""` | no |
| `references` | `str` | `string` | `""` | no |
| `plan_goal` | `str` | `string` | `""` | no |
| `plan_context` | `str` | `string` | `""` | no |
| `phase_context` | `str` | `string` | `""` | no |
| `parent_provider` | `str \| None` | `*string` | `null` | no |
| `initiative_id` | `str \| None` | `*string` | `null` | no |
| `flow_ref` | `str \| None` | `*string` | `null` | no |
| `flow_session_id` | `str \| None` | `*string` | `null` | no |
| `flow_template_id` | `str \| None` | `*string` | `null` | no |
| `flow_step_id` | `str \| None` | `*string` | `null` | no |
| `flow_step_type` | `str \| None` | `*string` | `null` | no |
| `flow_current_step` | `int \| None` | `*int` | `null` | no |
| `flow_delegated_sessions` | `List[str]` | `[]string` | `[]` | no |
| `flow_completed_at` | `datetime \| None` | `*time.Time` | `null` | no |
| `flow_outputs_summary` | `str \| None` | `*string` | `null` | no |
| `delegated_to` | `str \| None` | `*string` | `null` | no |
| `output` | `str \| None` | `*string` | `null` | no |
| `artifacts` | `List[dict]` | `[]Artifact` | `[]` | no |
| `created_at` | `datetime` | `time.Time` | `2026-05-04 17:55:23.871000+00:00` | no |
| `updated_at` | `datetime` | `time.Time` | `2026-05-04 17:55:23.871000+00:00` | no |
| `started_at` | `datetime \| None` | `*time.Time` | `null` | no |
| `claimed_at` | `datetime \| None` | `*time.Time` | `null` | no |
| `completed_at` | `datetime \| None` | `*time.Time` | `null` | no |
| `deleted` | `bool` | `bool` | `false` | **yes** |
| `deleted_at` | `datetime \| None` | `*time.Time` | `null` | **yes** |

*Indexes:* `task_id` (unique), `status`, `plan_id`, `epic_id`, `assignee`, `(assignee_type, assignee_id)`, `priority`, `created_at`, `(status, priority, created_at)`, `scheduled_for` (sparse), `initiative_id` (sparse).

### 10.2 `task_events`

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69f8dd8bf50771951a49c479` | no |
| `task_id` | `str` | `string` | `"t_94267a2"` | no |
| `sequence` | `int` | `int` | `1` | no |
| `event_type` | `str` | `string` | `"task_created"` | no |
| `timestamp` | `datetime` | `time.Time` | `2026-05-04 17:55:23.871000+00:00` | no |
| `actor` | `str` | `string` | `"silas"` | no |
| `data` | `dict` | `map[string]any` | `{"source":"plan_decomposition","plan_id":"deep-web-research-skill",…}` | no |
| `deleted` | `bool` | `bool` | `false` | **yes** |
| `deleted_at` | `datetime \| None` | `*time.Time` | `null` | **yes** |

*Indexes:* `(task_id, sequence)` (unique), `task_id`, `timestamp`.

### 10.3 `task_counters`

| Field | Python type | Go analogue | Sample | Soft-delete? |
|-------|-------------|-------------|--------|--------------|
| `_id` | `ObjectId` | `primitive.ObjectID` | `69f8dd8ba68d172ef0202367` | no |
| `task_id` | `str` | `string` | `"t_94267a2"` | no |
| `sequence` | `int` | `int` | `4` | no |
| `deleted` | `bool` | `bool` | `false` | **yes** |
| `deleted_at` | `datetime \| None` | `*time.Time` | `null` | **yes** |

*Note:* `task_counters` is a simple atomic counter document; the soft-delete wrapper is applied but the collection is tiny (one doc per task).

---

## Summary: Soft-Delete Participation Matrix

| Store | Collections | Soft-delete wrapper? | Notes |
|-------|-------------|----------------------|-------|
| taskstore | `pipelines`, `runs`, `artifacts` | **yes** | All three wrapped |
| planstore | `plans` | **yes** | |
| directivestore | `directives` | **yes** | |
| reportstore | `reports` | **yes** | |
| teamstore | `global.teams`, `global.agents`, `team_{id}.directives`, `team_{id}.initiatives` | **no** | Raw PyMongo; no wrapper used |
| flowstore | `session_flows` | **yes** | `flow_sessions` is **raw** |
| agent_memory | Qdrant `{agent}_memory` | **yes** (payload-level) | Not Mongo — Qdrant vector store |
| notifications | `global.notifications` | **yes** | |
| auth | `global.sessions` | **yes** | `users` is **raw** |
| taskqueue | `tasks`, `task_events`, `task_counters` | **yes** | All three wrapped |

---

## Edge Cases Flagged

1. **Empty collections** — `taskstore.pipelines` has no live samples in `silas` DB; shape inferred from `_connect()` + `list_pipelines()` query.
2. **In-memory fallback** — `flowstore` has `InMemoryFlowStore` (dict-backed) when `pymongo` is unavailable or unauthenticated. Mongo auth failed in the live probe (`code: 13 Unauthorized`), so the fallback path is real.
3. **Qdrant, not Mongo** — `agent_memory` uses Qdrant vector DB. Porting to Go needs `qdrant-client` (or a Mongo shadow collection).
4. **Raw collections with `deleted` fields** — `teamstore` (`global.agents`) and `auth` (`users`) have `deleted`/`deleted_at` in live docs but are **not** filtered by `SoftDeleteCollection`. Queries must manually filter if desired.
5. **Per-agent DB names** — `default` profile resolves to `silas` in Mongo. Go port must replicate `_resolve_db_name()` logic.
6. **Cross-DB access** — `TeamStore` uses `global` + dynamic `team_{id}` DBs from the same client. `TaskQueue.admin()` connects to `admin` DB but can access any DB via the client handle.
