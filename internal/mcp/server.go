package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Request is a JSON-RPC 2.0 request envelope.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// Response is a JSON-RPC 2.0 response envelope.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message)
}

// Tool describes an MCP tool.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Handler     func(ctx context.Context, raw json.RawMessage) (any, error)
}

// Server is a stdio MCP server.
type Server struct {
	name    string
	version string
	tools   map[string]Tool
}

// New creates a new Server.
func New(name, version string) *Server {
	return &Server{
		name:    name,
		version: version,
		tools:   make(map[string]Tool),
	}
}

// Register adds a tool to the server.
func (s *Server) Register(t Tool) {
	s.tools[t.Name] = t
}

// Serve runs the JSON-RPC loop over stdio using LSP-style framing.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	br := bufio.NewReader(in)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		body, err := readFramed(br)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			_ = writeFramed(out, mustJSON(Response{
				JSONRPC: "2.0",
				Error:   &RPCError{Code: CodeParseError, Message: err.Error()},
			}))
			continue
		}

		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			_ = writeFramed(out, mustJSON(Response{
				JSONRPC: "2.0",
				Error:   &RPCError{Code: CodeParseError, Message: err.Error()},
			}))
			continue
		}

		resp := s.dispatch(ctx, req)
		if err := writeFramed(out, mustJSON(resp)); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
	}
}

func (s *Server) dispatch(ctx context.Context, req Request) Response {
	resp := Response{JSONRPC: "2.0", ID: req.ID}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo": map[string]string{
				"name":    s.name,
				"version": s.version,
			},
			"capabilities": map[string]any{},
		}
	case "tools/list":
		type toolInfo struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		}
		tools := make([]toolInfo, 0, len(s.tools))
		for _, t := range s.tools {
			tools = append(tools, toolInfo{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
		resp.Result = map[string]any{"tools": tools}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			resp.Error = &RPCError{Code: CodeInvalidParams, Message: err.Error()}
			return resp
		}
		tool, ok := s.tools[params.Name]
		if !ok {
			resp.Error = &RPCError{Code: CodeMethodNotFound, Message: "tool not found: " + params.Name}
			return resp
		}
		result, err := callWithRecover(ctx, tool.Handler, params.Arguments)
		if err != nil {
			if rpcErr, ok := err.(*RPCError); ok {
				resp.Error = rpcErr
			} else {
				resp.Error = &RPCError{Code: CodeInternalError, Message: err.Error()}
			}
			return resp
		}
		resp.Result = map[string]any{"content": []map[string]string{{"type": "text", "text": fmt.Sprintf("%v", result)}}}
	default:
		resp.Error = &RPCError{Code: CodeMethodNotFound, Message: "method not found: " + req.Method}
	}

	return resp
}

func callWithRecover(ctx context.Context, handler func(context.Context, json.RawMessage) (any, error), args json.RawMessage) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return handler(ctx, args)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// ReadFramed reads an LSP-style framed message from an io.Reader.
// It wraps the reader in a bufio.Reader if needed.
// Format: Content-Length: <N>\r\n\r\n<body>
func ReadFramed(r io.Reader) ([]byte, error) {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return readFramed(br)
}

// WriteFramed writes an LSP-style framed message to w.
func WriteFramed(w io.Writer, body []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

// readFramed reads an LSP-style framed message from a *bufio.Reader.
// Format: Content-Length: <N>\r\n\r\n<body>
func readFramed(br *bufio.Reader) ([]byte, error) {

	// Read headers until we find Content-Length.
	var contentLength int64 = -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if err == io.EOF && line == "" {
				return nil, io.EOF
			}
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break // end of headers
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if strings.EqualFold(key, "Content-Length") {
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length: %w", err)
			}
			contentLength = n
		}
	}

	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}

	body := make([]byte, contentLength)
	_, err := io.ReadFull(br, body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// writeFramed writes an LSP-style framed message to w.
func writeFramed(w io.Writer, body []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}
