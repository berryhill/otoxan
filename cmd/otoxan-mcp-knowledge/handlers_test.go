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
	"github.com/silas/otoxan/internal/store/plans"
	"github.com/silas/otoxan/internal/store/reports"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func setupKnowledgeStores(t *testing.T) (*memory.MemoryStore, *plans.PlanStore, *reports.ReportStore, func()) {
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
	memStore := memory.NewMemoryStore(client.Database("otoxan").Collection("memories"), nil)
	planStore := plans.NewPlanStore(client.Database("otoxan").Collection("plans"))
	reportStore := reports.NewReportStore(client.Database("otoxan").Collection("reports"))
	cleanup := func() {
		_ = client.Disconnect(ctx)
		_ = ctr.Terminate(ctx)
	}
	return memStore, planStore, reportStore, cleanup
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

func TestKnowledgeMCP_Initialize(t *testing.T) {
	memStore, planStore, reportStore, cleanup := setupKnowledgeStores(t)
	defer cleanup()

	srv := mcp.New("otoxan-mcp-knowledge", "0.1.0")
	registerTools(srv, memStore, planStore, reportStore)

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
	if info["name"] != "otoxan-mcp-knowledge" {
		t.Fatalf("name = %v, want otoxan-mcp-knowledge", info["name"])
	}
}

func TestKnowledgeMCP_Search(t *testing.T) {
	memStore, planStore, reportStore, cleanup := setupKnowledgeStores(t)
	defer cleanup()

	ctx := context.Background()
	_, err := memStore.Create(ctx, &memory.Memory{MemoryID: "m1", AgentID: "a1", Content: "hello world memory", Type: memory.TypeObservation, CreatedAt: mustTime("2024-01-01T00:00:00Z"), UpdatedAt: mustTime("2024-01-01T00:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = planStore.Create(ctx, &plans.Plan{PlanID: "p1", Title: "hello plan", Content: "plan content", Status: plans.StatusPlanning, CreatedAt: mustTime("2024-01-01T00:00:00Z"), UpdatedAt: mustTime("2024-01-01T00:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	_, err = reportStore.Create(ctx, &reports.Report{ReportID: "r1", Title: "hello report", Content: "report content", Status: reports.StatusDraft, CreatedAt: mustTime("2024-01-01T00:00:00Z"), UpdatedAt: mustTime("2024-01-01T00:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}

	srv := mcp.New("otoxan-mcp-knowledge", "0.1.0")
	registerTools(srv, memStore, planStore, reportStore)

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
		"name":      "search",
		"arguments": map[string]any{"query": "hello", "k": 5},
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

func TestKnowledgeMCP_EmptyQuery(t *testing.T) {
	memStore, planStore, reportStore, cleanup := setupKnowledgeStores(t)
	defer cleanup()

	srv := mcp.New("otoxan-mcp-knowledge", "0.1.0")
	registerTools(srv, memStore, planStore, reportStore)

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
		"name":      "search",
		"arguments": map[string]any{"query": ""},
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
	if r.Error == nil {
		t.Fatal("expected error for empty query")
	}
	if r.Error.Code != mcp.CodeInvalidParams {
		t.Fatalf("code = %d, want %d", r.Error.Code, mcp.CodeInvalidParams)
	}
}

func mustTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
