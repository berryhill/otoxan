# Contributing to otoxan

Thank you for your interest in contributing. This document covers the basics.

## Getting Started

1. Fork the repository.
2. Clone your fork.
3. Ensure you have Go 1.23+ and MongoDB running locally (or use MongoDB Atlas).
4. Run `make build` to compile all binaries.
5. Run `make test` to execute the test suite.

## Development Workflow

- Create a feature branch from `main`.
- Write tests for new store methods or CLI commands.
- Run `make lint` before opening a PR.
- Keep commits focused and atomic.
- Open a pull request with a clear description of the change and the problem it solves.

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`).
- Use `golangci-lint` if available.
- Document exported types and functions with Go doc comments.
- Prefer explicit error handling over panics.

## Testing

- Unit tests live alongside source files (`*_test.go`).
- Store tests use an in-memory MongoDB instance via `testcontainers-go`
  when possible; otherwise they expect `MONGO_URI` in the environment.
- Run `make test` (which uses `-race`) before every commit.

## Documentation

- Update `docs/*.md` when you change architecture, configuration, or store semantics.
- Update `CHANGELOG.md` under the `[Unreleased]` section.
- Run `npx markdownlint-cli "**/*.md" --ignore node_modules` before opening a PR.

## Questions?

Open a discussion on GitHub or reach out via the contact method in `SECURITY.md`.
