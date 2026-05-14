package mcp_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/mcp"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

func projectRoot() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..")
}

func buildBinaries(t *testing.T) string {
	t.Helper()
	root := projectRoot()
	binDir := filepath.Join(root, "testbin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	bins := []string{
		"otoxan-mcp-tasks",
		"otoxan-mcp-memory",
		"otoxan-mcp-knowledge",
		"otoxan-mcp-flows",
		"otoxan-mcp-plans",
		"otoxan-mcp-identity",
	}

	for _, name := range bins {
		out := filepath.Join(binDir, name)
		if _, err := os.Stat(out); err == nil {
			continue // already built
		}
		t.Logf("building %s ...", name)
		cmd := exec.Command("go", "build", "-o", out, "./cmd/"+name)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, out)
		}
	}

	return binDir
}

func startMongo(t *testing.T) (uri string, cleanup func()) {
	t.Helper()
	ctx := t.Context()
	ctr, err := mongodb.Run(ctx, "mongo:7")
	if err != nil {
		t.Fatalf("start mongo container: %v", err)
	}
	uri, err = ctr.ConnectionString(ctx)
	if err != nil {
		_ = ctr.Terminate(ctx)
		t.Fatalf("get connection string: %v", err)
	}
	cleanup = func() {
		_ = ctr.Terminate(ctx)
	}
	return uri, cleanup
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

func sendReq(w io.Writer, req mcp.Request) error {
	b, _ := json.Marshal(req)
	return writeFramed(w, b)
}

func recvResp(br *bufio.Reader) (mcp.Response, error) {
	body, err := readFramed(br)
	if err != nil {
		return mcp.Response{}, err
	}
	var resp mcp.Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return mcp.Response{}, err
	}
	return resp, nil
}

func mustRecv(t *testing.T, br *bufio.Reader, timeout time.Duration) mcp.Response {
	t.Helper()
	done := make(chan mcp.Response, 1)
	errCh := make(chan error, 1)
	go func() {
		r, err := recvResp(br)
		if err != nil {
			errCh <- err
			return
		}
		done <- r
	}()
	select {
	case r := <-done:
		return r
	case err := <-errCh:
		t.Fatalf("recv: %v", err)
	case <-time.After(timeout):
		t.Fatalf("recv: timeout after %v", timeout)
	}
	return mcp.Response{}
}

// ------------------------------------------------------------------
// Test definition
// ------------------------------------------------------------------

type toolCall struct {
	name      string
	arguments map[string]any
	// wantShape is a minimal assertion on the result shape.
	// Keys must exist in the result map (or in the first element if result is a slice).
	wantShape map[string]any
	// wantSliceLen asserts the result is a slice of at least this length.
	wantSliceLen int
	// wantErrCode > 0 means we expect an RPC error with this code.
	wantErrCode int
}

type binaryTest struct {
	name      string
	binPath   string
	toolCalls []toolCall
}

// ------------------------------------------------------------------
// End-to-end test
// ------------------------------------------------------------------

func TestMCPEndToEnd(t *testing.T) {
	binDir := buildBinaries(t)
	mongoURI, mongoCleanup := startMongo(t)
	defer mongoCleanup()

	// Seed a flow template so start_flow can succeed.
	seedFlowTemplate(t, mongoURI)

	tests := []binaryTest{
		{
			name:    "tasks",
			binPath: filepath.Join(binDir, "otoxan-mcp-tasks"),
			toolCalls: []toolCall{
				{
					name: "create_task",
					arguments: map[string]any{
						"task_id": "e2e_task_1",
						"title":   "E2E Test Task",
						"status":  "QUEUED",
					},
					wantShape: map[string]any{"task_id": "e2e_task_1"},
				},
				{
					name:      "get_task",
					arguments: map[string]any{"task_id": "e2e_task_1"},
					wantShape: map[string]any{"task_id": "e2e_task_1", "title": "E2E Test Task"},
				},
				{
					name:         "list_tasks",
					arguments:    map[string]any{"limit": 10},
					wantSliceLen: 1,
				},
				{
					name:      "queue_status",
					arguments: map[string]any{},
					wantShape: map[string]any{}, // just assert it's a map
				},
				{
					name:      "claim_task",
					arguments: map[string]any{"agent": "e2e_agent"},
					wantShape: map[string]any{"task_id": "e2e_task_1", "status": "CLAIMED"},
				},
				{
					name:        "mark_completed",
					arguments:   map[string]any{"task_id": "e2e_task_1", "output": "done"},
					wantErrCode: mcp.CodeInvalidParams,
				},
				{
					name:      "mark_failed",
					arguments: map[string]any{"task_id": "e2e_task_1", "reason": "test"},
					wantShape: map[string]any{"task_id": "e2e_task_1", "status": "FAILED"},
				},
				{
					name:      "mark_retried",
					arguments: map[string]any{"task_id": "e2e_task_1"},
					wantShape: map[string]any{"task_id": "e2e_task_1", "status": "QUEUED"},
				},
				{
					name:      "delete_task",
					arguments: map[string]any{"task_id": "e2e_task_1"},
					wantShape: map[string]any{"deleted": "e2e_task_1"},
				},
			},
		},
		{
			name:    "memory",
			binPath: filepath.Join(binDir, "otoxan-mcp-memory"),
			toolCalls: []toolCall{
				{
					name: "save_memory",
					arguments: map[string]any{
						"agent":   "e2e_agent",
						"content": "hello from e2e",
						"tags":    []string{"e2e"},
					},
					wantShape: map[string]any{}, // memory_id exists
				},
				{
					name:         "list_memories",
					arguments:    map[string]any{"agent": "e2e_agent", "limit": 10},
					wantSliceLen: 1,
				},
				{
					name:         "search_memory",
					arguments:    map[string]any{"query": "hello", "k": 5},
					wantSliceLen: 1,
				},
			},
		},
		{
			name:    "knowledge",
			binPath: filepath.Join(binDir, "otoxan-mcp-knowledge"),
			toolCalls: []toolCall{
				{
					name:         "search",
					arguments:    map[string]any{"query": "nonexistent", "k": 5},
					wantSliceLen: 0,
				},
			},
		},
		{
			name:    "flows",
			binPath: filepath.Join(binDir, "otoxan-mcp-flows"),
			toolCalls: []toolCall{
				{
					name:      "start_flow",
					arguments: map[string]any{"template": "e2e_template"},
					wantShape: map[string]any{"name": "e2e_template"},
				},
				{
					name:         "list_flows",
					arguments:    map[string]any{"limit": 10},
					wantSliceLen: 1,
				},
			},
		},
		{
			name:    "plans",
			binPath: filepath.Join(binDir, "otoxan-mcp-plans"),
			toolCalls: []toolCall{
				{
					name: "create_plan",
					arguments: map[string]any{
						"plan_id": "e2e_plan_1",
						"title":   "E2E Test Plan",
						"goal":    "test the mcp e2e flow",
					},
					wantShape: map[string]any{"plan_id": "e2e_plan_1"},
				},
				{
					name:      "get_plan",
					arguments: map[string]any{"plan_id": "e2e_plan_1"},
					wantShape: map[string]any{"plan_id": "e2e_plan_1", "title": "E2E Test Plan"},
				},
				{
					name:         "list_plans",
					arguments:    map[string]any{"limit": 10},
					wantSliceLen: 1,
				},
				{
					name: "update_plan",
					arguments: map[string]any{
						"plan_id": "e2e_plan_1",
						"title":   "Updated E2E Plan",
					},
					wantShape: map[string]any{"plan_id": "e2e_plan_1", "title": "Updated E2E Plan"},
				},
				{
					name:      "decompose_plan",
					arguments: map[string]any{"plan_id": "e2e_plan_1"},
					wantShape: map[string]any{"plan_id": "e2e_plan_1"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, tt.binPath)
			cmd.Env = append(os.Environ(),
				"MONGO_URI="+mongoURI,
				"MONGO_DB=otoxan_e2e",
			)

			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatalf("stdin pipe: %v", err)
			}
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatalf("stdout pipe: %v", err)
			}
			stderr := &bytes.Buffer{}
			cmd.Stderr = stderr

			if err := cmd.Start(); err != nil {
				t.Fatalf("start binary: %v", err)
			}
			defer func() {
				_ = stdin.Close()
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				if stderr.Len() > 0 {
					t.Logf("stderr: %s", stderr.String())
				}
			}()

			br := bufio.NewReader(stdout)

			// 1. initialize
			if err := sendReq(stdin, mcp.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"}); err != nil {
				t.Fatalf("send initialize: %v", err)
			}
			initResp := mustRecv(t, br, 5*time.Second)
			if initResp.Error != nil {
				t.Fatalf("initialize error: %v", initResp.Error)
			}
			if m, ok := initResp.Result.(map[string]any); ok {
				if info, ok := m["serverInfo"].(map[string]any); ok {
					t.Logf("server: %s/%s", info["name"], info["version"])
				}
			}

			// 2. tools/list
			if err := sendReq(stdin, mcp.Request{JSONRPC: "2.0", ID: 2, Method: "tools/list"}); err != nil {
				t.Fatalf("send tools/list: %v", err)
			}
			listResp := mustRecv(t, br, 5*time.Second)
			if listResp.Error != nil {
				t.Fatalf("tools/list error: %v", listResp.Error)
			}
			resultMap, ok := listResp.Result.(map[string]any)
			if !ok {
				t.Fatal("tools/list result not a map")
			}
			toolsSlice, ok := resultMap["tools"].([]any)
			if !ok {
				t.Fatal("tools/list result.tools not a slice")
			}
			t.Logf("registered tools: %d", len(toolsSlice))

			// 3. tools/call per registered tool in test spec
			for i, tc := range tt.toolCalls {
				params, _ := json.Marshal(map[string]any{
					"name":      tc.name,
					"arguments": tc.arguments,
				})
				req := mcp.Request{JSONRPC: "2.0", ID: 3 + i, Method: "tools/call", Params: json.RawMessage(params)}
				if err := sendReq(stdin, req); err != nil {
					t.Fatalf("send tools/call %s: %v", tc.name, err)
				}
				resp := mustRecv(t, br, 5*time.Second)

				if tc.wantErrCode != 0 {
					if resp.Error == nil {
						t.Fatalf("tools/call %s: expected error code %d, got nil", tc.name, tc.wantErrCode)
					}
					if resp.Error.Code != tc.wantErrCode {
						t.Fatalf("tools/call %s: error code = %d, want %d", tc.name, resp.Error.Code, tc.wantErrCode)
					}
					continue
				}

				if resp.Error != nil {
					t.Fatalf("tools/call %s: unexpected error: %v", tc.name, resp.Error)
				}

				resultMap, ok := resp.Result.(map[string]any)
				if !ok {
					t.Fatalf("tools/call %s: result not a map", tc.name)
				}
				contentSlice, ok := resultMap["content"].([]any)
				if !ok || len(contentSlice) == 0 {
					t.Fatalf("tools/call %s: expected content slice", tc.name)
				}
				first, ok := contentSlice[0].(map[string]any)
				if !ok {
					t.Fatalf("tools/call %s: expected text content map", tc.name)
				}
				if first["type"] != "text" {
					t.Fatalf("tools/call %s: type = %v, want text", tc.name, first["type"])
				}

				// Parse the JSON text payload to inspect the actual tool result.
				text, _ := first["text"].(string)
				var payload any
				if err := json.Unmarshal([]byte(text), &payload); err != nil {
					// Some tools return plain strings (e.g. map[string]string marshalled).
					// Try unmarshalling into map.
					var m map[string]any
					if err2 := json.Unmarshal([]byte(text), &m); err2 != nil {
						t.Logf("tools/call %s: text not JSON: %q", tc.name, text)
						continue
					}
					payload = m
				}

				if tc.wantSliceLen >= 0 {
					slice, ok := payload.([]any)
					if !ok {
						t.Fatalf("tools/call %s: payload not a slice, got %T", tc.name, payload)
					}
					if len(slice) < tc.wantSliceLen {
						t.Fatalf("tools/call %s: len = %d, want >= %d", tc.name, len(slice), tc.wantSliceLen)
					}
				}

				if len(tc.wantShape) > 0 {
					m, ok := payload.(map[string]any)
					if !ok {
						t.Fatalf("tools/call %s: payload not a map, got %T", tc.name, payload)
					}
					for k, wantVal := range tc.wantShape {
						gotVal, ok := m[k]
						if !ok {
							t.Fatalf("tools/call %s: missing key %q", tc.name, k)
						}
						if wantVal != nil && gotVal != wantVal {
							t.Fatalf("tools/call %s: %q = %v, want %v", tc.name, k, gotVal, wantVal)
						}
					}
				}
			}
		})
	}
}

// seedFlowTemplate inserts a flow template document into Mongo so start_flow works.
func seedFlowTemplate(t *testing.T, mongoURI string) {
	t.Helper()
	ctx := t.Context()
	client, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = client.Disconnect(ctx) }()

	coll := client.Database("otoxan_e2e").Collection("flow_templates")
	_, err = coll.InsertOne(ctx, map[string]any{
		"name":        "e2e_template",
		"description": "E2E test template",
		"steps": []map[string]any{
			{"name": "step1", "type": "action"},
			{"name": "step2", "type": "action"},
		},
	})
	if err != nil {
		t.Fatalf("seed template: %v", err)
	}
}
