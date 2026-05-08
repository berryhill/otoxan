# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Top-level `otoxan` CLI with 11 subcommands: init, task, plan, team, flow, memory, dispatch, worker, mcp, serve, version.
- Config loader supporting YAML + `OTOXAN_*` environment variables with strict-mode option.
- MongoDB auth with Infisical fallback for secrets.
- Task store with soft-delete, CRUD, and list filtering.
- Plan store with soft-delete and status lifecycle.
- `otoxan init` interactive bootstrap for first-time setup.
- MCP server binaries: otoxan-mcp-tasks, otoxan-mcp-memory, otoxan-mcp-knowledge, otoxan-mcp-flows.
- Documentation: README, architecture, stores, dispatch, MCP, configuration.

### Changed

- Replaced all hardcoded `/home/silas/.hermes/` paths with `OTOXAN_HOME` / `XDG_DATA_HOME` resolution.
- Removed embedded admin client secret from taskstore; now sourced from Infisical or env.

### Fixed

- n/a

### Deprecated

- n/a

### Removed

- n/a

### Security

- n/a
