# Identity Manifest Schema

> Spec for versioned persona documents stored in `otoxan_global.identities`.
> The store layer (identitystore) enforces the shape and lifecycle described here.

---

## Overview

An **Identity** is a named persona — a system-prompt payload plus routing metadata — that can be injected into any model caller. Identities are versioned: editing creates a new version; the active version is what callers receive.

The store is a single MongoDB collection (`otoxan_global.identities`) with one document per identity+version pair.

---

## Data Structure (DS-1)

### Top-level fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `_id` | `ObjectId` | yes | MongoDB document ID (not used in API) |
| `identity_id` | `string` | yes | Stable identifier for the persona (e.g. `"xander"`) |
| `version` | `int` | yes | Monotonically increasing integer per `identity_id` |
| `status` | `string` | yes | One of `DRAFT`, `ACTIVE`, `RETIRED` |
| `name` | `string` | yes | Human-readable display name (e.g. `"Xander"`) |
| `description` | `string` | no | One-line summary of who this persona is |
| `system_prompt` | `string` | yes | The persona's core instruction text — plain text, no envelope |
| `provider_profiles` | `map[string]ProviderProfile` | yes | Per-provider adapter configs (see below) |
| `created_at` | `datetime` | yes | When this version was first saved |
| `updated_at` | `datetime` | yes | When this version was last modified |
| `created_by` | `string` | yes | Agent or user who created this version |
| `activated_at` | `datetime` | no | When this version became `ACTIVE` |
| `retired_at` | `datetime` | no | When this version became `RETIRED` |

### ProviderProfile shape

Mirrors OpenClaude's `agentRouting` pattern: `{role → providerProfileName}` mapping.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `provider` | `string` | yes | One of: `anthropic`, `openai`, `ollama`, `gemini` |
| `role` | `string` | yes | Always `"system"` for persona injection |
| `envelope` | `string` | yes | Envelope format: see Per-Provider Envelopes below |
| `cache_breakpoint` | `bool` | no | If `true`, inject Anthropic `cache_control` hint |
| `model_hints` | `[]string` | no | Preferred models (e.g. `["claude-3-5-sonnet-latest"]`) |
| `extra` | `map[string]any` | no | Provider-specific extensions |

#### Per-Provider Envelopes

| Provider | Envelope | Description |
|----------|----------|-------------|
| `anthropic` | `"top_level_system"` | Passed as `system` param at API call top level; supports `cache_control: {type: "ephemeral"}` |
| `openai` | `"messages_system"` | Emitted as `{"role": "system", "content": "…"}` inside the messages array |
| `ollama` | `"chat_template"` | Written into the per-model chat template (caller supplies template) |
| `gemini` | `"system_instruction"` | Passed as `system_instruction` in the GenerateContent request |

---

## Status Lifecycle

```
DRAFT ────► ACTIVE ────► RETIRED
  │            ▲
  └────────────┘
      (edit)
```

### DRAFT

- New versions start as `DRAFT`.
- DRAFT versions are readable and editable but **not returned** by `GetActive`.
- Multiple DRAFT versions may coexist for the same `identity_id`.

### ACTIVE

- Exactly **one** version per `identity_id` may be `ACTIVE` at a time.
- Activating a version auto-retires any previously active version.
- `GetActive(identity_id)` returns the current active version (or `nil` if none).
- `ListVersions(identity_id)` returns all versions regardless of status.

### RETIRED

- Retired versions are immutable — the store rejects updates to `system_prompt` or `provider_profiles` after retirement.
- `GetActive` never returns a RETIRED version.
- Retirement enables audit trail: "Xander v3 said this in production" is queryable.
- Retirement does NOT delete the document.

### Allowed transitions

| From | To | Via |
|------|----|-----|
| `DRAFT` | `ACTIVE` | `Activate(id)` |
| `ACTIVE` | `RETIRED` | `Retire(id)` |
| `DRAFT` | `RETIRED` | `Retire(id)` (direct) |

No reverse transitions (e.g. `RETIRED → ACTIVE`). To re-activate a retired version, create a new version from it.

---

## Versioning Rules

1. **Immutable content after activation.** Once a version is `ACTIVE`, its `system_prompt` and `provider_profiles` fields are locked. To change the active persona, create a new version and activate it.

2. **Version numbers are monotonic.** The store assigns `version = max(existing versions) + 1` on create. Gaps from deleted/test versions are fine — versions are identified by `_id`, not by number gaps.

3. **Active uniqueness enforced at DB level.** A unique compound index on `(identity_id, status)` with a partial filter `{status: "ACTIVE"}` ensures exactly one active version at rest.

4. **Rollback is new-version creation.** There is no "re-activate v3" operation. To roll back: read v3's payload, `Create(v4)` with that content, then `Activate(v4_id)`. This preserves the audit log showing the rollback event.

5. **Audit log is implicit.** Each document carries `created_at`, `activated_at`, `retired_at`, and `created_by`. Querying the collection by `identity_id` ordered by `created_at` yields the full history.

---

## Indexes

| Index | Fields | Unique | Partial |
|-------|--------|--------|---------|
| `identity_id + version` | `(identity_id, version)` | yes | — |
| `identity_id + status` | `(identity_id, status)` | no | `{"status": "ACTIVE"}` |
| `created_at` | `(created_at)` | no | — |

---

## Out of Scope (Not in DS-1)

These features require separate specs and are explicitly excluded from MVP:

- Memory-aware identity (persona adapts based on interaction history)
- Identity evolution / learning loops
- Eval harness (PersonaGym-style comparison)
- Multi-shot exemplars baked into the manifest
- Cross-framework export (CrewAI YAML, Claude Code agent files)
- Binding identities to specific agents (agent→identity mapping is a caller-side concern, not a store concern)
