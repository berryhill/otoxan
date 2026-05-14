# `~/.otoxan/` Install Layout Reference

This document is the canonical reference for every path owned by the otoxan install layer. It is the single source of truth for what lives on disk under `~/.otoxan/`.

## Directory structure

```
~/.otoxan/
├── bin/
│   └── otoxan              # The otoxan binary itself
├── config.yaml             # User configuration stub (created on init, never overwritten)
├── version                 # Installed version string (plain text, one line)
├── logs/                   # Runtime log output from long-lived agents and jobs
└── cache/                  # Temporary / working data (e.g., downloaded release tarballs)
```

## Path reference

| Path | Type | Purpose | Created by |
|------|------|---------|------------|
| `~/.otoxan/` | Directory | Install home. Controlled by `$OTOXAN_HOME` env var, defaults to `$HOME/.otoxan`. | `otoxan init` |
| `~/.otoxan/bin/` | Directory | Binary directory. Added to `$PATH` by install scripts. | `otoxan init` |
| `~/.otoxan/bin/otoxan` | File | The otoxan binary. Self-replaced atomically by `otoxan update`. | Install script / `otoxan update` |
| `~/.otoxan/config.yaml` | File | User configuration stub. Created once on init; never overwritten (preserved on `--force`). | `otoxan init` |
| `~/.otoxan/version` | File | Installed version string (e.g., `0.3.1`). Plain text, one line, newline-terminated. Read by support diagnostics without executing the binary. | `otoxan init`, updated by `otoxan update` |
| `~/.otoxan/logs/` | Directory | Runtime log output. Long-lived agents and scheduled jobs write here. | `otoxan init` |
| `~/.otoxan/cache/` | Directory | Temporary working data (e.g., downloaded release tarballs before extraction). Safe to delete. | `otoxan init` |

## Environment variable

- `$OTOXAN_HOME` — If set and non-empty, used verbatim as the install home. Overrides the default `~/.otoxan/`.

## Invariants

1. **Stateless install layer.** No state files are written under `~/.otoxan/`. All runtime state (agents, plans, tasks, memory) lives in MongoDB. This is the load-bearing invariant of the base plate.
2. **Idempotent init.** Running `otoxan init` twice is safe: it ensures directories exist and updates the version file, but does not overwrite an existing `config.yaml`.
3. **Atomic binary replacement.** `otoxan update` writes the new binary to a temp file in the same directory, then renames over the target. There is no window where the binary is partially written.
4. **Version file is always present.** The version file is written on every init and update so that `otoxan update` can detect "already on latest" and support can read the installed version without executing a potentially corrupted binary.

## DS-1: Not in this directory

The following are **explicitly excluded** from `~/.otoxan/`. Future plans must not re-invent state-on-disk. If you need to store any of these, use MongoDB (added in `otoxan-state-layer`).

- **Agent registry / agent definitions** — lives in MongoDB.
- **Plans, tasks, initiatives** — lives in MongoDB.
- **Memory / conversation history** — lives in MongoDB.
- **User secrets / API keys** — lives in MongoDB (encrypted at rest) or the system keychain, not in `config.yaml`.
- **Database files (SQLite, LevelDB, etc.)** — lives in MongoDB.
- **Lock files / PID files** — use OS-level mechanisms (flock, systemd) instead of disk state.
- **Session state / flow state** — lives in MongoDB.
- **Git repositories / cloned code** — belongs in `~/code/` or workspace directories, not under `~/.otoxan/`.
- **Docker images / container layers** — managed by the container runtime, not otoxan.
- **Schema migrations** — managed by `xander migrate-schema`, not the install layer.

## Rationale

### Why `~/.otoxan/` and not `~/.config/otoxan/`

Matches the `~/.hermes` precedent deliberately to keep mental model overlap during the cutover. XDG migration is a future concern; not worth adding now.

### Why a global directory at all

Otoxan has long-lived agents, scheduled jobs, and per-user persona/keys. Per-workspace `./.otoxan/` (like `.git`) would be wrong — agents are user-global, not project-scoped.

### Why `otoxan update` is binary-only

Wider scope (Docker images, schema migrations, managed-repo pulls) means more failure modes and harder rollback. Binary-only is what every shippable Go CLI does (`gh extension upgrade`, `rustup update`, `bun upgrade`). Image refresh + schema migrations belong on Xander capabilities (`xander upgrade-agent`, `xander migrate-schema`), not the platform self-updater.

### Why a version file at all

So `otoxan update` can detect "already on latest", and so support diagnostics can read the installed version without running the binary (which may be corrupted post-failed-update).
