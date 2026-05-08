# otoxan

AI agent operations CLI — tasks, plans, teams, flows, memory, dispatch, workers, and MCP servers.

## TL;DR

```bash
go install github.com/silas/otoxan/cmd/otoxan@latest
otoxan init
otoxan task list
```

## Install

Requires Go 1.23+ and a MongoDB instance (local or Atlas).

```bash
# Install the main CLI
go install github.com/silas/otoxan/cmd/otoxan@latest

# (Optional) Install all MCP servers
go install github.com/silas/otoxan/cmd/otoxan-mcp-tasks@latest
go install github.com/silas/otoxan/cmd/otoxan-mcp-memory@latest
go install github.com/silas/otoxan/cmd/otoxan-mcp-knowledge@latest
go install github.com/silas/otoxan/cmd/otoxan-mcp-flows@latest
```

Binaries land in `$(go env GOPATH)/bin`. Ensure that directory is on your `PATH`.

## Quickstart

### 1. Bootstrap configuration

```bash
otoxan init
```

This creates `$XDG_DATA_HOME/otoxan/config.yaml` (or `~/.local/share/otoxan/config.yaml`)
after prompting for Mongo URI, database name, default agent, and Infisical settings.

### 2. Verify connectivity

```bash
otoxan task list
```

If MongoDB is reachable and the `tasks` collection exists, you will see a JSON array of tasks (possibly empty).

### 3. Create your first task

```bash
otoxan task create hello-world \
  --title "Hello World" \
  --description "First otoxan task" \
  --status QUEUED
```

### 4. Explore subcommands

```bash
otoxan --help
otoxan task --help
otoxan plan --help
otoxan dispatch --help
otoxan mcp --help
```

## Architecture

otoxan is a Go rewrite of the legacy Hermes Python system. It is built on three layers:

1. **Foundation** — `autozan-go-foundation` provides shared MongoDB connectivity,
   soft-delete wrappers, logging, and configuration primitives.
2. **Stores** — MongoDB-backed document stores (tasks, plans, teams, flows, memory,
   knowledge) with BSON shapes matching the Python originals.
3. **Dispatch** — A goroutine-based task dispatcher that replaces the single-threaded
   Python polling loop. It manages slot counting, spawn tracking, and MCP worker
   lifecycle in memory, writing to MongoDB only for durable state transitions.

MCP servers are standalone binaries that communicate over stdio (JSON-RPC 2.0 /
newline-delimited). They expose tools for CRUD on each store domain.

For deeper detail, see the `docs/` directory:

- `docs/architecture.md` — System overview and component diagram
- `docs/stores.md` — Store inventory and BSON field maps
- `docs/dispatch.md` — Goroutine topology and channel diagram
- `docs/mcp.md` — MCP server spec and transport notes
- `docs/configuration.md` — Config file schema and environment variables

## Configuration

Configuration is loaded from two sources, in order of increasing precedence:

1. `$OTOXAN_HOME/config.yaml` (or `$XDG_DATA_HOME/otoxan/config.yaml`)
2. Environment variables prefixed with `OTOXAN_`

Key variables:

| Variable | Purpose | Default |
|----------|---------|---------|
| `OTOXAN_HOME` | Config directory | `$XDG_DATA_HOME/otoxan` |
| `OTOXAN_MONGO_URI` | MongoDB connection string | *(from config or Infisical)* |
| `OTOXAN_MONGO_DB` | Database name | `otoxan` |
| `OTOXAN_INFISICAL_TOKEN` | Infisical service token | *(none)* |
| `OTOXAN_INFISICAL_PROJECT_ID` | Infisical project ID | *(none)* |
| `OTOXAN_INFISICAL_ENV` | Infisical environment | `dev` |
| `OTOXAN_STRICT_MODE` | Reject unknown env vars | `false` |

See `docs/configuration.md` for the full YAML schema.

## Status

This is an early-stage rewrite. The CLI surface is stabilizing; the dispatch daemon and MCP servers are under active development.

| Component | Status |
|-----------|--------|
| CLI (`otoxan`) | Functional — init, task, plan, team, flow, memory, dispatch, worker, mcp, serve, version |
| Config loader | Stable |
| Mongo auth (Infisical fallback) | Stable |
| Task store | Stable |
| Plan store | Stable |
| Dispatch daemon | In progress |
| MCP servers | In progress |
| Web UI | Not started |

## License

MIT — see [LICENSE](LICENSE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) and [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## Security

See [SECURITY.md](SECURITY.md) for vulnerability reporting.
