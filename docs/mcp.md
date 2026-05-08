# MCP Servers

Reference: `docs/mcp-spec-notes.md`

## Overview

MCP (Model Context Protocol) servers are standalone binaries that expose tools for CRUD
on each otoxan store domain. They communicate with clients over stdio using JSON-RPC 2.0
(newline-delimited).

## Binaries

| Binary | Domain | Status |
|--------|--------|--------|
| `otoxan-mcp-tasks` | Tasks | Functional |
| `otoxan-mcp-memory` | Memory | Functional |
| `otoxan-mcp-knowledge` | Knowledge | Functional |
| `otoxan-mcp-flows` | Flows | Functional |

## Transport

- Server is launched as a subprocess by the client.
- Server reads JSON-RPC from **stdin**, writes to **stdout**.
- Messages are delimited by newlines (NDJSON).
- Server MAY write UTF-8 strings to **stderr** for logging.
- Server MUST NOT write anything to stdout that is not a valid MCP message.

### Framing Format

```
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{...}}\n
```

### Compatibility Note

Some clients send `Content-Length: N\r\n\r\n` followed by the JSON body
(LSP-style). Robust servers accept both formats:

1. **Primary (spec-compliant):** read line-by-line, parse each line as JSON.
2. **Fallback (LSP-style):** if a line starts with `Content-Length:`, read the length,
   consume the `\r\n\r\n` separator, then read exactly N bytes.

## JSON-RPC 2.0 Envelope

### Request

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/list",
  "params": {}
}
```

### Response (success)

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "tools": [
      {
        "name": "task_create",
        "description": "Create a new task",
        "inputSchema": { ... }
      }
    ]
  }
}
```

### Notification (no `id`)

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/initialized",
  "params": {}
}
```

## Mandatory Methods

A tools-only server must implement:

1. `initialize` — Must be the first interaction. Server returns capabilities.
2. `notifications/initialized` — Client sends after receiving initialize response.
3. `tools/list` — Returns available tools.
4. `tools/call` — Executes a tool by name with given arguments.

## Tools by Domain

### Tasks

- `task_create` — Create a new task
- `task_get` — Get a task by ID
- `task_list` — List tasks with optional filters
- `task_update` — Update task fields
- `task_delete` — Soft-delete a task

### Memory

- `memory_create` — Store a memory entry
- `memory_get` — Retrieve a memory entry
- `memory_search` — Search memories by content or tags
- `memory_delete` — Remove a memory entry

### Knowledge

- `knowledge_create` — Store a knowledge document
- `knowledge_get` — Retrieve a document
- `knowledge_search` — Search documents by content
- `knowledge_delete` — Remove a document

### Flows

- `flow_create` — Create a flow session
- `flow_get` — Get a session by ID
- `flow_list` — List sessions
- `flow_update` — Update session state
- `flow_delete` — Remove a session

## Spec Version

Pinned to MCP specification version **2025-06-18**.
Source: https://modelcontextprotocol.io/specification/2025-06-18
