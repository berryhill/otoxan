package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/mcp"
	"github.com/silas/otoxan/internal/store/memory"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func setupMemoryStore(t *testing.T) (*memory.MemoryStore, func()) {
	t.Helper()
	ctx := context.Background()
	ctr, err := mongodb.Run(ctx, "mongo:7")
	if err != nil {
		t.Fatalf("start mongo container: %v", err)
	}
	uri, err := ctr.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	coll := client.Database("otoxan").Collection("memories")
	store := memory.NewMemoryStore(coll, nil)
	cleanup := func() {
		_ = client.Disconnect(ctx)
		_ = ctr.Terminate(ctx)
	}
	return store, cleanup
}

func encodeReq(req mcp.Request) string {
	b, _ := json.Marshal(req)
	var buf bytes.Buffer
	_ = writeFramed(&buf, b)
	return buf.String()
}

func writeFramed(w io.Writer, body []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := io.WriteString(w, header); err != nil {
		return err
	}
	_, err := w.Write(body)
	return err
}

func readFramed(br *bufio.Reader) ([]byte, error) {
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
			break
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

func TestMemoryMCP_Initialize(t *testing.T) {
	store, cleanup := setupMemoryStore(t)
	defer cleanup()

	srv := mcp.New("otoxan-mcp-memory", "0.1.0")
	registerTools(srv, store)

	pr, pw := io.Pipe()
	var out bytes.Buffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, pr, &out)
	}()

	req := encodeReq(mcp.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	<-done

	resp, err := readFramed(bufio.NewReader(&out))
	if err != nil {
		t.Fatal(err)
	}
	var r mcp.Response
	if err := json.Unmarshal(resp, &r); err != nil {
		t.Fatal(err)
	}
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	m, ok := r.Result.(map[string]any)
	if !ok {
		t.Fatal("result not a map")
	}
	info, ok := m["serverInfo"].(map[string]any)
	if !ok {
		t.Fatal("serverInfo not a map")
	}
	if info["name"] != "otoxan-mcp-memory" {
		t.Fatalf("name = %v, want otoxan-mcp-memory", info["name"])
	}
}

func TestMemoryMCP_SaveMemory(t *testing.T) {
	store, cleanup := setupMemoryStore(t)
	defer cleanup()

	srv := mcp.New("otoxan-mcp-memory", "0.1.0")
	registerTools(srv, store)

	pr, pw := io.Pipe()
	var out bytes.Buffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, pr, &out)
	}()

	params, _ := json.Marshal(map[string]any{
		"name": "save_memory",
		"arguments": map[string]any{
			"agent":   "test-agent",
			"content": "hello world",
			"tags":    []string{"greeting"},
		},
	})
	req := encodeReq(mcp.Request{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: json.RawMessage(params)})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	<-done

	resp, err := readFramed(bufio.NewReader(&out))
	if err != nil {
		t.Fatal(err)
	}
	var r mcp.Response
	if err := json.Unmarshal(resp, &r); err != nil {
		t.Fatal(err)
	}
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	resultMap, ok := r.Result.(map[string]any)
	if !ok {
		t.Fatal("result not a map")
	}
	contentSlice, ok := resultMap["content"].([]any)
	if !ok || len(contentSlice) == 0 {
		t.Fatal("expected content slice")
	}
	first, ok := contentSlice[0].(map[string]any)
	if !ok {
		t.Fatal("expected text content map")
	}
	if first["type"] != "text" {
		t.Fatalf("type = %v, want text", first["type"])
	}
}

func TestMemoryMCP_ListMemories(t *testing.T) {
	store, cleanup := setupMemoryStore(t)
	defer cleanup()

	ctx := context.Background()
	_, err := store.Create(ctx, &memory.Memory{MemoryID: "m1", AgentID: "a1", Content: "c1", Type: memory.TypeObservation, CreatedAt: mustTime("2024-01-01T00:00:00Z"), UpdatedAt: mustTime("2024-01-01T00:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create(ctx, &memory.Memory{MemoryID: "m2", AgentID: "a1", Content: "c2", Type: memory.TypeObservation, CreatedAt: mustTime("2024-01-02T00:00:00Z"), UpdatedAt: mustTime("2024-01-02T00:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}

	srv := mcp.New("otoxan-mcp-memory", "0.1.0")
	registerTools(srv, store)

	pr, pw := io.Pipe()
	var out bytes.Buffer

	ctx2, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx2, pr, &out)
	}()

	params, _ := json.Marshal(map[string]any{
		"name":      "list_memories",
		"arguments": map[string]any{"agent": "a1", "limit": 10},
	})
	req := encodeReq(mcp.Request{JSONRPC: "2.0", ID: 2, Method: "tools/call", Params: json.RawMessage(params)})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	<-done

	resp, err := readFramed(bufio.NewReader(&out))
	if err != nil {
		t.Fatal(err)
	}
	var r mcp.Response
	if err := json.Unmarshal(resp, &r); err != nil {
		t.Fatal(err)
	}
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
}

func TestMemoryMCP_SearchMemory(t *testing.T) {
	store, cleanup := setupMemoryStore(t)
	defer cleanup()

	ctx := context.Background()
	_, err := store.Create(ctx, &memory.Memory{MemoryID: "m1", AgentID: "a1", Content: "hello world", Type: memory.TypeObservation, CreatedAt: mustTime("2024-01-01T00:00:00Z"), UpdatedAt: mustTime("2024-01-01T00:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}

	srv := mcp.New("otoxan-mcp-memory", "0.1.0")
	registerTools(srv, store)

	pr, pw := io.Pipe()
	var out bytes.Buffer

	ctx2, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx2, pr, &out)
	}()

	params, _ := json.Marshal(map[string]any{
		"name":      "search_memory",
		"arguments": map[string]any{"query": "hello", "k": 5},
	})
	req := encodeReq(mcp.Request{JSONRPC: "2.0", ID: 3, Method: "tools/call", Params: json.RawMessage(params)})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	<-done

	resp, err := readFramed(bufio.NewReader(&out))
	if err != nil {
		t.Fatal(err)
	}
	var r mcp.Response
	if err := json.Unmarshal(resp, &r); err != nil {
		t.Fatal(err)
	}
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
}

func TestMemoryMCP_InvalidParams(t *testing.T) {
	store, cleanup := setupMemoryStore(t)
	defer cleanup()

	srv := mcp.New("otoxan-mcp-memory", "0.1.0")
	registerTools(srv, store)

	pr, pw := io.Pipe()
	var out bytes.Buffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, pr, &out)
	}()

	// Missing required agent field
	params, _ := json.Marshal(map[string]any{
		"name":      "save_memory",
		"arguments": map[string]any{"content": "foo"},
	})
	req := encodeReq(mcp.Request{JSONRPC: "2.0", ID: 4, Method: "tools/call", Params: json.RawMessage(params)})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	<-done

	resp, err := readFramed(bufio.NewReader(&out))
	if err != nil {
		t.Fatal(err)
	}
	var r mcp.Response
	if err := json.Unmarshal(resp, &r); err != nil {
		t.Fatal(err)
	}
	if r.Error == nil {
		t.Fatal("expected error for missing agent")
	}
	if r.Error.Code != mcp.CodeInvalidParams {
		t.Fatalf("code = %d, want %d", r.Error.Code, mcp.CodeInvalidParams)
	}
}

func mustTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
