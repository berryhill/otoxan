package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"

	"github.com/silas/otoxan/internal/mcp"
	"github.com/silas/otoxan/internal/store/queue"
	"github.com/silas/otoxan/internal/store/tasks"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// In-memory test harness
// ------------------------------------------------------------------

func setupTestServer(t *testing.T) (*mcp.Server, *tasks.TaskStore, *queue.TaskQueue, func()) {
	ctx := context.Background()

	container, err := mongodb.Run(ctx, "mongo:7")
	if err != nil {
		t.Skipf("testcontainers mongodb not available: %v", err)
	}

	uri, err := container.ConnectionString(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		t.Skipf("failed to get connection string: %v", err)
	}

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		_ = container.Terminate(ctx)
		t.Skipf("mongo connect failed: %v", err)
	}

	db := client.Database("otoxan_test_mcp_tasks")
	_ = db.Collection("tasks").Drop(ctx)
	_ = db.Collection("task_events").Drop(ctx)
	_ = db.Collection("task_counters").Drop(ctx)

	taskStore := tasks.NewTaskStore(db.Collection("tasks"))
	taskQueue := queue.NewTaskQueue(db, taskStore)

	srv := mcp.New("otoxan-tasks-test", "0.1.0")
	registerTools(srv, taskStore, taskQueue)

	cleanup := func() {
		_ = client.Disconnect(ctx)
		_ = container.Terminate(ctx)
	}

	return srv, taskStore, taskQueue, cleanup
}

// safeBuffer wraps bytes.Buffer with a mutex for concurrent access.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *safeBuffer) Write(p []byte) (n int, err error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *safeBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

func (sb *safeBuffer) Reader() *bytes.Reader {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return bytes.NewReader(sb.buf.Bytes())
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func encodeReq(req request) string {
	b, _ := json.Marshal(req)
	var buf bytes.Buffer
	_ = mcp.WriteFramed(&buf, b)
	return buf.String()
}

func readResponse(t *testing.T, sb *safeBuffer) mcp.Response {
	t.Helper()
	resp, err := mcp.ReadFramed(sb.Reader())
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var r mcp.Response
	if err := json.Unmarshal(resp, &r); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return r
}

// ------------------------------------------------------------------
// Tool tests
// ------------------------------------------------------------------

func TestToolCreateAndGetTask(t *testing.T) {
	srv, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, pr, &out)
	}()

	// Create task
	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params: json.RawMessage(`{"name":"create_task","arguments":{"task_id":"t1","title":"Test task","description":"desc","assignee":"silas"}}`),
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}

	// Get task
	req2 := encodeReq(request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params: json.RawMessage(`{"name":"get_task","arguments":{"task_id":"t1"}}`),
	})
	if _, err := pw.Write([]byte(req2)); err != nil {
		t.Fatal(err)
	}

	pw.Close()
	<-done

	// Read both responses
	br := bufio.NewReader(out.Reader())
	resp1Bytes, err := mcp.ReadFramed(br)
	if err != nil {
		t.Fatalf("read resp1: %v", err)
	}
	var resp1 mcp.Response
	if err := json.Unmarshal(resp1Bytes, &resp1); err != nil {
		t.Fatalf("unmarshal resp1: %v", err)
	}
	if resp1.Error != nil {
		t.Fatalf("create_task error: %v", resp1.Error)
	}

	resp2Bytes, err := mcp.ReadFramed(br)
	if err != nil {
		t.Fatalf("read resp2: %v", err)
	}
	var resp2 mcp.Response
	if err := json.Unmarshal(resp2Bytes, &resp2); err != nil {
		t.Fatalf("unmarshal resp2: %v", err)
	}
	if resp2.Error != nil {
		t.Fatalf("get_task error: %v", resp2.Error)
	}

	resultMap, ok := resp2.Result.(map[string]any)
	if !ok {
		t.Fatal("result not a map")
	}
	content, ok := resultMap["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatal("content missing")
	}
}

func TestToolListTasks(t *testing.T) {
	srv, store, _, cleanup := setupTestServer(t)
	defer cleanup()
	ctx := context.Background()

	// Seed tasks
	_, _ = store.Create(ctx, &tasks.Task{TaskID: "lt1", Title: "A", Status: tasks.StatusQueued, Assignee: "silas"})
	_, _ = store.Create(ctx, &tasks.Task{TaskID: "lt2", Title: "B", Status: tasks.StatusCompleted, Assignee: "silas"})

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx2, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx2, pr, &out)
	}()

	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"list_tasks","arguments":{"agent":"silas","status":["QUEUED"]}}`),
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	<-done

	r := readResponse(t, &out)
	if r.Error != nil {
		t.Fatalf("list_tasks error: %v", r.Error)
	}
}

func TestToolUpdateAndDeleteTask(t *testing.T) {
	srv, store, _, cleanup := setupTestServer(t)
	defer cleanup()
	ctx := context.Background()

	_, _ = store.Create(ctx, &tasks.Task{TaskID: "ud1", Title: "Original", Status: tasks.StatusQueued})

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx2, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx2, pr, &out)
	}()

	// Update
	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"update_task","arguments":{"task_id":"ud1","title":"Updated"}}`),
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}

	// Delete
	req2 := encodeReq(request{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"delete_task","arguments":{"task_id":"ud1"}}`),
	})
	if _, err := pw.Write([]byte(req2)); err != nil {
		t.Fatal(err)
	}

	pw.Close()
	<-done

	br := bufio.NewReader(out.Reader())
	resp1Bytes, _ := mcp.ReadFramed(br)
	var resp1 mcp.Response
	_ = json.Unmarshal(resp1Bytes, &resp1)
	if resp1.Error != nil {
		t.Fatalf("update error: %v", resp1.Error)
	}

	resp2Bytes, _ := mcp.ReadFramed(br)
	var resp2 mcp.Response
	_ = json.Unmarshal(resp2Bytes, &resp2)
	if resp2.Error != nil {
		t.Fatalf("delete error: %v", resp2.Error)
	}
}

func TestToolClaimAndComplete(t *testing.T) {
	srv, store, tq, cleanup := setupTestServer(t)
	defer cleanup()
	ctx := context.Background()

	_, _ = store.Create(ctx, &tasks.Task{TaskID: "cq1", Title: "Claim me", Status: tasks.StatusQueued})

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx2, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx2, pr, &out)
	}()

	// Claim
	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"claim_task","arguments":{"agent":"silas"}}`),
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}

	pw.Close()
	<-done

	r := readResponse(t, &out)
	if r.Error != nil {
		t.Fatalf("claim error: %v", r.Error)
	}

	// Mark running first (via direct store update since MarkRunning is not exposed as tool)
	_, _ = tq.Tasks().Update(ctx, "cq1", bson.M{"status": tasks.StatusRunning})

	// Now mark completed in a fresh server session
	pr2, pw2 := io.Pipe()
	var out2 safeBuffer

	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()

	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		_ = srv.Serve(ctx3, pr2, &out2)
	}()

	req2 := encodeReq(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"mark_completed","arguments":{"task_id":"cq1","output":"done"}}`),
	})
	if _, err := pw2.Write([]byte(req2)); err != nil {
		t.Fatal(err)
	}
	pw2.Close()
	<-done2

	r2 := readResponse(t, &out2)
	if r2.Error != nil {
		t.Fatalf("mark_completed error: %v", r2.Error)
	}
}

func TestToolMarkFailedAndRetried(t *testing.T) {
	srv, store, _, cleanup := setupTestServer(t)
	defer cleanup()
	ctx := context.Background()

	_, _ = store.Create(ctx, &tasks.Task{TaskID: "fr1", Title: "Fail me", Status: tasks.StatusRunning})

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx2, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx2, pr, &out)
	}()

	// Mark failed
	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"mark_failed","arguments":{"task_id":"fr1","reason":"oops"}}`),
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}

	pw.Close()
	<-done

	r := readResponse(t, &out)
	if r.Error != nil {
		t.Fatalf("mark_failed error: %v", r.Error)
	}

	// Mark retried
	pr2, pw2 := io.Pipe()
	var out2 safeBuffer

	ctx3, cancel3 := context.WithCancel(context.Background())
	defer cancel3()

	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		_ = srv.Serve(ctx3, pr2, &out2)
	}()

	req2 := encodeReq(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"mark_retried","arguments":{"task_id":"fr1"}}`),
	})
	if _, err := pw2.Write([]byte(req2)); err != nil {
		t.Fatal(err)
	}
	pw2.Close()
	<-done2

	r2 := readResponse(t, &out2)
	if r2.Error != nil {
		t.Fatalf("mark_retried error: %v", r2.Error)
	}
}

func TestToolQueueStatus(t *testing.T) {
	srv, store, _, cleanup := setupTestServer(t)
	defer cleanup()
	ctx := context.Background()

	_, _ = store.Create(ctx, &tasks.Task{TaskID: "qs1", Title: "Q1", Status: tasks.StatusQueued})
	_, _ = store.Create(ctx, &tasks.Task{TaskID: "qs2", Title: "Q2", Status: tasks.StatusCompleted})

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx2, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx2, pr, &out)
	}()

	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"queue_status","arguments":{}}`),
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	<-done

	r := readResponse(t, &out)
	if r.Error != nil {
		t.Fatalf("queue_status error: %v", r.Error)
	}
}

func TestToolGetRunnable(t *testing.T) {
	srv, store, _, cleanup := setupTestServer(t)
	defer cleanup()
	ctx := context.Background()

	_, _ = store.Create(ctx, &tasks.Task{TaskID: "gr1", Title: "Run me", Status: tasks.StatusQueued})

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx2, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx2, pr, &out)
	}()

	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"get_runnable","arguments":{"agent":"silas"}}`),
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	<-done

	r := readResponse(t, &out)
	if r.Error != nil {
		t.Fatalf("get_runnable error: %v", r.Error)
	}
}

func TestToolMissingRequiredField(t *testing.T) {
	srv, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, pr, &out)
	}()

	// get_task without task_id
	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"get_task","arguments":{}}`),
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	<-done

	r := readResponse(t, &out)
	if r.Error == nil {
		t.Fatal("expected error for missing task_id")
	}
	if r.Error.Code != mcp.CodeInvalidParams {
		t.Fatalf("code = %d, want %d", r.Error.Code, mcp.CodeInvalidParams)
	}
}

func TestToolNotFound(t *testing.T) {
	srv, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, pr, &out)
	}()

	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"get_task","arguments":{"task_id":"nonexistent"}}`),
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	<-done

	r := readResponse(t, &out)
	if r.Error == nil {
		t.Fatal("expected error for nonexistent task")
	}
	if r.Error.Code != mcp.CodeInvalidParams {
		t.Fatalf("code = %d, want %d", r.Error.Code, mcp.CodeInvalidParams)
	}
}

func TestToolsList(t *testing.T) {
	srv, _, _, cleanup := setupTestServer(t)
	defer cleanup()

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, pr, &out)
	}()

	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	<-done

	r := readResponse(t, &out)
	if r.Error != nil {
		t.Fatalf("tools/list error: %v", r.Error)
	}

	resultMap, ok := r.Result.(map[string]any)
	if !ok {
		t.Fatal("result not a map")
	}
	tools, ok := resultMap["tools"].([]any)
	if !ok {
		t.Fatal("tools not an array")
	}
	if len(tools) != 11 {
		t.Fatalf("expected 11 tools, got %d", len(tools))
	}
}

// ------------------------------------------------------------------
// Helper: extract text from tool result for assertions
// ------------------------------------------------------------------

func resultText(r mcp.Response) string {
	if r.Result == nil {
		return ""
	}
	m, ok := r.Result.(map[string]any)
	if !ok {
		return ""
	}
	content, ok := m["content"].([]any)
	if !ok || len(content) == 0 {
		return ""
	}
	c, ok := content[0].(map[string]any)
	if !ok {
		return ""
	}
	txt, _ := c["text"].(string)
	return txt
}
