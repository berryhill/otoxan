# MCP Spec Notes — otoxan hand-rolled Go servers

> Research artefact for building five `cmd/otoxan-mcp-X` binaries.  
> Pinned to MCP specification version **2025-06-18**.  
> Source: https://modelcontextprotocol.io/specification/2025-06-18

---

## 1. Framing / Transport (stdio)

The spec defines **stdio** as the primary transport.  Key rules:

| Rule | Spec citation |
|------|---------------|
| Server is launched as a **subprocess** by the client. | Transports §stdio |
| Server reads JSON-RPC from **stdin**, writes to **stdout**. | Transports §stdio |
| Messages are **delimited by newlines** (NDJSON). | Transports §stdio |
| Messages **MUST NOT** contain embedded newlines. | Transports §stdio |
| Server **MAY** write UTF-8 strings to **stderr** for logging. | Transports §stdio |
| Server **MUST NOT** write anything to stdout that is not a valid MCP message. | Transports §stdio |
| Client **MUST NOT** write anything to stdin that is not a valid MCP message. | Transports §stdio |
| JSON-RPC messages **MUST** be UTF-8 encoded. | Transports preamble |

**Framing format:**
```
{"jsonrpc":"2.0",...}\n
```

**No `Content-Length` header in the official spec.**  
The spec explicitly says newline-delimited JSON.  However, **real-world compatibility note**: some clients built on the TypeScript SDK (and LSP-influenced code) send `Content-Length: N\r\n\r\n` followed by the JSON body without a trailing newline.  The Python SDK issue #2546 (May 2026) documents this gap.  For maximum interoperability a robust server SHOULD accept **both** formats:

1. **Primary (spec-compliant):** read line-by-line, parse each line as JSON.
2. **Fallback (LSP-style):** if a line starts with `Content-Length:`, read the length, consume the `\r\n\r\n` separator, then read exactly N bytes as the JSON body.

---

## 2. JSON-RPC 2.0 Envelope

Every message is a JSON-RPC 2.0 object.

### Request
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": { ... }
}
```

### Response (success)
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": { ... }
}
```

### Response (error)
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32600,
    "message": "Invalid Request",
    "data": { ... }
  }
}
```

### Notification (no `id`)
```json
{
  "jsonrpc": "2.0",
  "method": "notifications/initialized",
  "params": { ... }
}
```

**ID rules:**
- Requests have an `id` (string or number).  
- Notifications omit `id`.  
- Responses echo the `id` of the request they answer.  
- The `initialize` request **MUST NOT** be cancelled.

---

## 3. Mandatory Methods for a Tools-Only Server

### 3.1 `initialize`
- **Direction:** client → server (request)
- **Must be the first interaction.**
- Client sends:
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-06-18",
    "capabilities": { },
    "clientInfo": { "name": "...", "version": "..." }
  }
}
```
- Server responds with:
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2025-06-18",
    "capabilities": {
      "tools": { "listChanged": false }
    },
    "serverInfo": { "name": "otoxan-mcp-X", "version": "0.1.0" }
  }
}
```
- After successful response, client sends `notifications/initialized`.
- Until `initialized`, client **SHOULD NOT** send requests other than pings.

### 3.2 `notifications/initialized`
- **Direction:** client → server (notification)
- No response required.
```json
{
  "jsonrpc": "2.0",
  "method": "notifications/initialized"
}
```

### 3.3 `tools/list`
- **Direction:** client → server (request)
- Returns the array of available tools.
- Request:
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/list",
  "params": { }
}
```
- Response:
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "tools": [
      {
        "name": "tool_name",
        "description": "...",
        "inputSchema": { "type": "object", "properties": { ... } }
      }
    ]
  }
}
```

### 3.4 `tools/call`
- **Direction:** client → server (request)
- Invokes a tool.
- Request:
```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "tool_name",
    "arguments": { "arg1": "value1" }
  }
}
```
- Response (`result.content` is an array of content objects):
```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "content": [
      { "type": "text", "text": "..." }
    ],
    "isError": false
  }
}
```

### 3.5 `ping` (optional but recommended)
- **Direction:** either side
- Used to keep connection alive / detect zombies.
- Request: `{"jsonrpc":"2.0","id":N,"method":"ping"}`
- Response: `{"jsonrpc":"2.0","id":N,"result":{}}`

---

## 4. Error Codes (JSON-RPC 2.0 + MCP)

| Code | Name | Meaning |
|------|------|---------|
| -32700 | Parse error | Invalid JSON was received. |
| -32600 | Invalid Request | The JSON sent is not a valid Request object. |
| -32601 | Method not found | The method does not exist / is not available. |
| -32602 | Invalid params | Invalid method parameter(s). |
| -32603 | Internal error | Internal JSON-RPC error. |
| -32000 to -32099 | Server error | Reserved for implementation-defined server errors. |

MCP-specific: if a tool call fails at the application level, return `isError: true` in the `tools/call` result (not a JSON-RPC error).  JSON-RPC errors are for protocol-level problems.

---

## 5. Reference Open-Source Servers (cross-check)

### 5.1 `@modelcontextprotocol/server-filesystem`
- Repo: https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem
- Language: TypeScript
- Uses `StdioServerTransport` from the official TypeScript SDK.
- Key pattern: `new McpServer(...)` → `server.tool(name, schema, handler)` → `server.connect(new StdioServerTransport())`.
- **Framing:** The TypeScript SDK historically used `Content-Length` headers for stdio (LSP style).  As of the 2025-06-18 spec the official stance is NDJSON, but SDK behaviour may vary by version.

### 5.2 `mark3labs/mcp-go`
- Repo: https://github.com/mark3labs/mcp-go
- Language: Go
- Community Go SDK.  Implements stdio transport with a simple scanner loop.
- Good reference for Go-specific JSON-RPC wiring.

### 5.3 `modelcontextprotocol/python-sdk` (official Python SDK)
- Repo: https://github.com/modelcontextprotocol/python-sdk
- Uses line-by-line NDJSON reading in `mcp/server/stdio.py`.
- Known issue (#2546, May 2026): fails on `Content-Length` header format from TypeScript clients.  This confirms the **dual-format reader** recommendation above.

---

## 6. Edge Cases & Decisions

| Concern | Decision for otoxan |
|---------|---------------------|
| **Spec version drift** | Pin to `2025-06-18`.  Re-audit when a new stable spec is released. |
| **Framing variant** | Implement **both** NDJSON (spec) and `Content-Length` (TypeScript SDK compat).  Default reader tries NDJSON first; if a line starts with `Content-Length`, switch to LSP-style parsing for that message. |
| **Embedded newlines in JSON** | Reject — spec says MUST NOT contain embedded newlines.  If encountered, return Parse error (-32700). |
| **stderr logging** | Use `log/slog` with output to stderr; never write non-protocol data to stdout. |
| **Cancellation** | Support `notifications/cancelled` for long-running tool calls.  `initialize` is non-cancellable per spec. |
| **Capability negotiation** | Declare only `tools` capability.  No prompts, resources, or sampling. |
| **Timeouts** | No spec-mandated timeout; use context.Context in Go for internal deadlines. |

---

## 7. Minimal Go stdio server skeleton

```go
package main

import (
    "bufio"
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "os"
    "strconv"
    "strings"
)

type JSONRPCMessage struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      any             `json:"id,omitempty"`
    Method  string          `json:"method,omitempty"`
    Params  json.RawMessage `json:"params,omitempty"`
    Result  json.RawMessage `json:"result,omitempty"`
    Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
    Data    any    `json:"data,omitempty"`
}

func main() {
    stdin := bufio.NewReader(os.Stdin)
    for {
        msg, err := readMessage(stdin)
        if err == io.EOF {
            return
        }
        if err != nil {
            writeError(nil, -32700, "Parse error")
            continue
        }
        handle(msg)
    }
}

func readMessage(r *bufio.Reader) (*JSONRPCMessage, error) {
    // Try NDJSON first
    line, err := r.ReadString('\n')
    if err != nil {
        return nil, err
    }
    line = strings.TrimSpace(line)
    if line == "" {
        return readMessage(r) // skip empty lines
    }
    // LSP-style fallback
    if strings.HasPrefix(line, "Content-Length:") {
        parts := strings.SplitN(line, ":", 2)
        n, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
        // consume \r\n\r\n
        for i := 0; i < 2; i++ {
            _, _ = r.ReadString('\n')
        }
        body := make([]byte, n)
        _, err := io.ReadFull(r, body)
        if err != nil {
            return nil, err
        }
        var msg JSONRPCMessage
        err = json.Unmarshal(body, &msg)
        return &msg, err
    }
    var msg JSONRPCMessage
    err = json.Unmarshal([]byte(line), &msg)
    return &msg, err
}

func writeMessage(msg *JSONRPCMessage) {
    b, _ := json.Marshal(msg)
    fmt.Println(string(b))
}

func writeError(id any, code int, message string) {
    writeMessage(&JSONRPCMessage{
        JSONRPC: "2.0",
        ID:      id,
        Error:   &JSONRPCError{Code: code, Message: message},
    })
}
```

---

*Document generated 2026-05-07.  Revisit when MCP spec 2025-11-25 (or later) stabilises.*
