# otoxan Dependency Versions

> Reference: DS-1 — Go workspace dependency lock for the persistence layer port.

## Core Dependencies

| Dependency | Version | License | Last Release |
|---|---|---|---|
| go.mongodb.org/mongo-driver/v2 | v2.6.0 | Apache-2.0 | Jan 2026 |
| github.com/testcontainers/testcontainers-go | v0.42.0 | Apache-2.0 | Jan 2026 |
| github.com/testcontainers/testcontainers-go/modules/mongodb | v0.42.0 | Apache-2.0 | Jan 2026 |

## Notes

- **mongo-driver v2**: GA released April 2024. v2.6.0 is the latest stable as of this lock. All 10 store ports (taskstore, planstore, teamstore, directivestore, reportstore, flowstore, agent_memory, notifications, auth, taskqueue) build against this ABI.
- **testcontainers-go**: The MongoDB module is a submodule that tracks the same version as the parent. v0.42.0 is the latest stable.
- Both dependencies are Apache-2.0 licensed, compatible with otoxan OSS.
- Minimum Go version required: 1.22 (workspace currently uses 1.23.5).
