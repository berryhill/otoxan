# Qdrant Indexing Reference

> Canonical reference for otoxan's Qdrant-based vector indexing.  
> Every meaningful thing an agent does — session messages, plans, tasks, code artifacts, build output, error traces — is embedded and stored in Qdrant within 60 seconds of writing the source-of-truth document to MongoDB. A single search surface (`otoxan recall`) returns ranked results across an agent's entire history.

---

## Table of Contents

1. [Collection Schema](#collection-schema)
2. [Payload Fields](#payload-fields)
3. [Supported Source Types](#supported-source-types)
4. [Embedding Provider Config](#embedding-provider-config)
5. [Ops Runbook](#ops-runbook)
6. [Architecture Notes](#architecture-notes)

---

## Collection Schema

### Naming convention

One Qdrant collection per agent:

```
{agent}_index          # unified index for all source types (this doc)
{agent}_memory         # semantic memory facts / episodes (legacy, still active)
{agent}_sessions       # session transcript turns (legacy, still active)
{agent}_knowledge      # agent knowledge index from MongoDB collections (legacy, still active)
```

The unified `agent_<n>_index` is the target collection for the "everything" indexing pipeline described in the plan context. Legacy collections remain for backward compatibility during cutover.

### Vector parameters

```go
// internal/qdrant/client.go — CreateCollectionRequest
Vectors: struct {
    Size     int    `json:"size"`
    Distance string `json:"distance"`
}{
    Size:     384,      // or 1536 for OpenAI text-embedding-3-small
    Distance: "Cosine",
}
```

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| `size` | 384 (default) or 1536 | 384 = fastembed / BAAI/bge-small-en-v1.5; 1536 = OpenAI text-embedding-3-small |
| `distance` | Cosine | Standard for dense semantic embeddings |
| `indexing_threshold` | 50 | Start HNSW indexing after 50 points (low-latency for small agents) |
| `on_disk_payload` | true | Keep payload on disk; vectors in RAM for fast search |

### Payload indexes

Created at collection setup time for every field used in filters:

```go
// internal/index/pipeline.go (conceptual) — payload indexes
fields := []struct {
    Name string
    Type string
}{
    {"source_type",  "keyword"},
    {"source_id",    "keyword"},
    {"agent",        "keyword"},
    {"chunk_index",  "integer"},
    {"title",        "text"},
    {"status",       "keyword"},
    {"created_at",   "float"},
    {"updated_at",   "float"},
}
```

---

## Payload Fields

Every point in `agent_<n>_index` carries the following payload:

| Field | Type | Description |
|-------|------|-------------|
| `source_type` | string | Kind of document: `session`, `plan`, `task`, `report`, `directive`, `artifact`, `task_event`, `notification`, `build`, `error`, `run` |
| `source_id` | string | MongoDB `_id` or slug of the source document |
| `agent` | string | Agent identifier (e.g. `silas`) |
| `title` | string | Human-readable title, if available |
| `content_preview` | string | First 300 characters of the chunk |
| `text` | string | Full chunk text (searchable, not returned by default) |
| `chunk_index` | int | Position of this chunk within the document |
| `total_chunks` | int | Total chunks for this `source_id` |
| `timestamp` | string (ISO) | Document `updated_at` or `created_at` |
| `indexed_at` | string (ISO) | When this point was last embedded and upserted |
| `tags` | []string | Document tags, if any |
| `status` | string | Document status, if applicable |
| `created_at` | string (ISO) | Document creation time |
| `updated_at` | string (ISO) | Document last-update time |
| `meta` | map[string]any | Remaining metadata (plan_id, priority, assignee, etc.) |

### Point ID generation

Deterministic UUID5 for idempotent upserts:

```go
NAMESPACE = uuid.MustParse("a1b2c3d4-e5f6-7890-abcd-ef1234567890")

func PointID(sourceType, sourceID string, chunkIndex int) string {
    return uuid.NewSHA1(NAMESPACE, []byte(
        fmt.Sprintf("%s:%s:%d", sourceType, sourceID, chunkIndex),
    )).String()
}
```

Same document + same chunk = same point ID. Re-indexing is naturally idempotent.

---

## Supported Source Types

The indexer pipeline (`internal/index/pipeline.go`, `cmd/otoxan-indexer/`) reads from MongoDB and writes to Qdrant. The following source types are supported:

| `source_type` | MongoDB collection | Text fields | Timestamp field |
|---------------|-------------------|-------------|-----------------|
| `plan` | `plans` | `title`, `content` | `updated_at` |
| `task` | `tasks` | `title`, `description`, `directive`, `intent`, `implementation`, `output` | `updated_at` |
| `report` | `reports` | `title`, `content` | `updated_at` |
| `directive` | `directives` | `title`, `content`, `rule`, `reason` | `updated_at` |
| `artifact` | `artifacts` | `name`, `description`, `content`, `text` | `updated_at` |
| `task_event` | `task_events` | `event_type` + stringified `data` | `timestamp` |
| `notification` | `notifications` | `task_title` | `created_at` |
| `flow_session` | `flow_sessions` | `notes` | `updated_at` |
| `session_flow` | `session_flows` | `name`, `description` | `updated_at` |
| `session` | `sessions` (SQLite → Mongo migration) | `user_content`, `assistant_content` | `timestamp` |
| `build` | `builds` | `output`, `error_trace` | `updated_at` |
| `error` | `errors` | `message`, `stack_trace` | `created_at` |
| `run` | `runs` | `status`, `error_message` | `updated_at` |

### Chunking strategy

- **Chunk size:** 512 characters
- **Overlap:** 64 characters
- **Boundary preference:** newline > space > hard cut
- **Batch embed size:** 32–64 chunks per `BatchEmbed` call (rate-limit friendly)

---

## Embedding Provider Config

### Interface

```go
// internal/embedder/embedder.go
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    BatchEmbed(ctx context.Context, texts []string) ([][]float32, error)
    Model() string
    Dimension() int
}
```

### Providers

| Provider | Model | Dimension | Config key | Notes |
|----------|-------|-----------|------------|-------|
| **fastembed** (default) | `BAAI/bge-small-en-v1.5` | 384 | `embedding.model = "bge-small"` | Local ONNX, zero API cost, ~130MB download |
| **OpenAI** | `text-embedding-3-small` | 1536 | `embedding.provider = "openai"` | Requires `OPENAI_API_KEY` or Infisical secret |
| **Ollama** | configurable | configurable | `embedding.provider = "ollama"` | Local HTTP, default `http://localhost:11434` |

### Config example (otoxan.toml)

```toml
[embedding]
provider = "fastembed"          # "fastembed" | "openai" | "ollama"
model = "bge-small-en-v1.5"    # provider-specific model name
dimension = 384                 # must match the model
batch_size = 32                 # chunks per BatchEmbed call

[embedding.openai]
base_url = "https://api.openai.com"
# api_key is resolved from OPENAI_API_KEY env var or Infisical

[embedding.ollama]
base_url = "http://localhost:11434"
model = "nomic-embed-text"
```

### Secret resolution order

1. Explicit field in config
2. Environment variable (`OPENAI_API_KEY`, `QDRANT_API_KEY`, etc.)
3. Infisical secret store (`auth.InfisicalGet(key)`)

---

## Ops Runbook

### Check index health

```bash
# Total points per agent
curl -s "http://localhost:6333/collections/silas_index" | jq '.result.points_count'

# Points per source_type
curl -s -X POST "http://localhost:6333/collections/silas_index/points/count" \
  -H "Content-Type: application/json" \
  -d '{"filter": {"must": [{"key": "source_type", "match": {"value": "task"}}]}}' \
  | jq '.result.count'
```

### Re-index a single collection

```bash
# Full reindex of tasks only
./otoxan-indexer --agent silas --source-type task --full

# Incremental (default) — only docs newer than last known timestamp
./otoxan-indexer --agent silas --source-type task
```

### Full re-index everything

```bash
./otoxan-indexer --agent silas --full
```

### Find stale pointers (MongoDB → Qdrant drift)

```bash
# The indexer exposes a /stale endpoint or CLI:
./otoxan-indexer --agent silas --find-stale
```

Returns `MemoryPointer` docs where `source_updated_at` < the source doc's current `updated_at`. The next incremental run will re-embed and upsert them.

### Delete orphaned Qdrant points

When a source doc is soft-deleted in MongoDB, the pointer doc is marked `removed=true`. A periodic cleaner job deletes the corresponding Qdrant point:

```bash
./otoxan-indexer --agent silas --clean-removed
```

### Reset a collection (nuclear option)

```bash
curl -X DELETE "http://localhost:6333/collections/silas_index"
# Then re-run full index
./otoxan-indexer --agent silas --full
```

### Monitor indexing lag

The `global.knowledge_index_state` collection (MongoDB) tracks per-agent, per-collection state:

```json
{
  "agent": "silas",
  "collection": "tasks",
  "last_timestamp": "2026-05-09T23:45:00Z",
  "last_id": "69fbcc287cd3464c52c438b0",
  "updated_at": "2026-05-09T23:50:12Z"
}
```

Lag = `now - last_timestamp`. Alert if > 90 seconds (budget is 60s).

### Common issues

| Symptom | Cause | Fix |
|---------|-------|-----|
| `UserWarning: Api key is used with an insecure connection` | Local HTTP Qdrant, no TLS | Harmless noise; do not fix |
| `openai rate limited` | Batch size too large or too frequent | Lower `batch_size` to 16; add 1s sleep between batches |
| `expected dimension 384, got 1536` | Config mismatch between embedder and collection | Drop collection and re-create with correct `VectorParams` |
| Orphaned points accumulate | No automatic cleanup after soft-delete | Run `--clean-removed` periodically or add cron job |
| Cold start latency (30–60s) | fastembed loading ONNX model | Expected; use `tee /tmp/indexer.log` and monitor |

---

## Architecture Notes

### Why a separate indexer process

Embedding takes 50–500ms per batch depending on the model. Doing it inline on every MongoDB write would make every write slow. The indexer runs as a standalone process (or cron job) that polls MongoDB change streams / timestamps, batches embed calls, and upserts to Qdrant. Write latency stays fast; index freshness stays within the 60-second budget.

### Why one collection per agent

Qdrant collections are cheap but cross-collection search is awkward. One collection (`agent_<n>_index`) with a `source_type` payload field means:

- "Search all of Alice's history" → one query, no filter
- "Search Alice's tasks only" → one query with `source_type = task` filter
- Same pattern Hermes' `session_messages` uses

### Why pointer docs in MongoDB

The `memory_pointers` collection (per-agent DB) records:

- `source_id` → `qdrant_point_id` mapping
- `source_updated_at` at time of indexing
- `indexed_at` timestamp
- `removed` flag for soft-delete propagation

This gives us:

1. **Re-index detection** — stale pointer triggers re-embed
2. **Deletion propagation** — soft-delete in MongoDB cascades to point removal in Qdrant
3. **Audit** — "when was this indexed?"

See `internal/index/pointer.go` for the Go struct and `internal/index/pointer_test.go` for testcontainers-backed tests.

### Batching

OpenAI and Anthropic both rate-limit embeddings. The indexer bun-ups 32 docs per `BatchEmbed` call instead of 32 sequential calls. For fastembed (local), batch size can go to 64 with no rate-limit penalty.

### 60-second freshness budget

Anything longer breaks the "I just did this; help me find it" reflex. Anything shorter forces inline embedding. 60s is the median between "feels live" and "doesn't kill write throughput."
