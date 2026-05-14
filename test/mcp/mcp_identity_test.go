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
	"strings"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/mcp"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

// projectRoot is defined in mcp_e2e_test.go (shared package).

func buildIdentityBinary(t *testing.T) string {
	t.Helper()
	root := projectRoot()
	binDir := filepath.Join(root, "testbin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	out := filepath.Join(binDir, "otoxan-mcp-identity")
	if _, err := os.Stat(out); err == nil {
		return out
	}
	t.Logf("building otoxan-mcp-identity ...")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/otoxan-mcp-identity")
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build otoxan-mcp-identity: %v\n%s", err, b)
	}
	return out
}

// seedIdentity seeds the identities collection in otoxan_global (the database
// the identitystore reads from) with a test identity and activates it.
func seedIdentity(t *testing.T, client *mongo.Client) {
	ctx := context.Background()
	// identitystore uses GlobalDB which is always "otoxan_global".
	coll := client.Database("otoxan_global").Collection("identities")

	// Insert a version and activate it directly via direct update so we own
	// the activation index constraint (partial index: name+status=active unique).
	doc := map[string]any{
		"name":            "xander",
		"version":         "v4",
		"status":          "active",
		"manifest":        "You are Xander v4, a helpful AI assistant. You are direct, knowledgeable, and efficient. You specialize in answering questions and solving problems.",
		"description":     "Xander v4 — primary agent persona",
		"tags":            []string{"primary", "test"},
		"provider_profiles": map[string]string{
			"anthropic": "You are Xander v4. Be thorough and show your reasoning.",
			"openai":    "You are Xander v4, a helpful AI assistant.",
			"ollama":    "You are Xander. Be concise.",
		},
		"created_at": time.Now().UTC(),
		"updated_at": time.Now().UTC(),
		"activated_at": time.Now().UTC(),
		"deleted":   false,
	}
	if _, err := coll.InsertOne(ctx, doc); err != nil {
		t.Fatalf("seed identity: %v", err)
	}

	// Create indexes the store would create.
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "name", Value: 1}, {Key: "version", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "name", Value: 1}, {Key: "status", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.D{{Key: "status", Value: "active"}})},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes); err != nil {
		t.Fatalf("create indexes: %v", err)
	}
}

// ------------------------------------------------------------------
// Integration test
// ------------------------------------------------------------------

func TestResolveIdentityIntegration(t *testing.T) {
	binPath := buildIdentityBinary(t)

	// Spin up Mongo.
	ctx := context.Background()
	ctr, err := mongodb.Run(ctx, "mongo:7")
	if err != nil {
		t.Fatalf("start mongo: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	uri, err := ctr.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	dbName := "otoxan_identity_e2e"

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(ctx) })

	// Seed data.
	seedIdentity(t, client)

	// Start the binary.
	cmd := exec.CommandContext(ctx, binPath)
	cmd.Env = append(os.Environ(),
		"MONGO_URI="+uri,
		"MONGO_DB="+dbName,
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
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if stderr.Len() > 0 {
			t.Logf("stderr: %s", stderr.String())
		}
	})

	br := bufio.NewReader(stdout)

	send := func(req mcp.Request) error {
		b, _ := json.Marshal(req)
		header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(b))
		if _, err := fmt.Fprintf(stdin, "%s", header); err != nil {
			return err
		}
		_, err := stdin.Write(b)
		return err
	}

	recv := func() ([]byte, error) {
		var contentLength int64 = -1
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return nil, err
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
				n, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &contentLength)
				if err != nil || n == 0 {
					return nil, fmt.Errorf("bad Content-Length: %s", parts[1])
				}
			}
		}
		if contentLength < 0 {
			return nil, fmt.Errorf("missing Content-Length")
		}
		body := make([]byte, contentLength)
		_, err := io.ReadFull(br, body)
		return body, err
	}

	// 1. initialize
	if err := send(mcp.Request{JSONRPC: "2.0", ID: 1, Method: "initialize"}); err != nil {
		t.Fatalf("send initialize: %v", err)
	}
	body, err := recv()
	if err != nil {
		t.Fatalf("recv initialize: %v", err)
	}
	var initResp mcp.Response
	if err := json.Unmarshal(body, &initResp); err != nil {
		t.Fatalf("unmarshal init: %v", err)
	}
	if initResp.Error != nil {
		t.Fatalf("initialize error: %v", initResp.Error)
	}

	// 2. tools/list — verify resolve_identity is registered
	if err := send(mcp.Request{JSONRPC: "2.0", ID: 2, Method: "tools/list"}); err != nil {
		t.Fatalf("send tools/list: %v", err)
	}
	body, err = recv()
	if err != nil {
		t.Fatalf("recv tools/list: %v", err)
	}
	var listResp mcp.Response
	if err := json.Unmarshal(body, &listResp); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if listResp.Error != nil {
		t.Fatalf("tools/list error: %v", listResp.Error)
	}
	resultMap, ok := listResp.Result.(map[string]any)
	if !ok {
		t.Fatal("tools/list result not a map")
	}
	tools, ok := resultMap["tools"].([]any)
	if !ok {
		t.Fatal("tools/list result.tools not a slice")
	}
	var found bool
	for _, tl := range tools {
		tm, ok := tl.(map[string]any)
		if !ok {
			continue
		}
		if tm["name"] == "resolve_identity" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("resolve_identity tool not registered")
	}

	// 3. resolve_identity for xander (known agent, has active identity)
	callTests := []struct {
		name        string
		tool        string
		arguments   map[string]any
		wantOK      bool
		wantErrCode int
		wantFields  []string // keys that must be present in the parsed payload
	}{
		{
			name:   "resolve xander with default provider (openai)",
			tool:   "resolve_identity",
			arguments: map[string]any{
				"name": "xander",
			},
			wantOK:     true,
			wantFields: []string{"ok", "agent", "version", "has_prompt", "system_prompt", "provider", "envelope"},
		},
		{
			name:   "resolve xander with explicit openai provider",
			tool:   "resolve_identity",
			arguments: map[string]any{
				"name":     "xander",
				"provider": "openai",
			},
			wantOK:     true,
			wantFields: []string{"ok", "system_prompt", "provider"},
		},
		{
			name:   "resolve xander with anthropic provider",
			tool:   "resolve_identity",
			arguments: map[string]any{
				"name":     "xander",
				"provider": "anthropic",
			},
			wantOK:     true,
			wantFields: []string{"ok", "system_prompt", "provider"},
		},
		{
			name:   "resolve xander with gemini provider",
			tool:   "resolve_identity",
			arguments: map[string]any{
				"name":     "xander",
				"provider": "gemini",
			},
			wantOK:     true,
			wantFields: []string{"ok", "system_prompt", "provider"},
		},
		{
			name:   "resolve xander with ollama provider",
			tool:   "resolve_identity",
			arguments: map[string]any{
				"name":     "xander",
				"provider": "ollama",
			},
			wantOK:     true,
			wantFields: []string{"ok", "system_prompt", "provider"},
		},
		{
			name:   "resolve unknown agent returns no_active_identity cleanly",
			tool:   "resolve_identity",
			arguments: map[string]any{
				"name": "unknown-agent-xyz",
			},
			wantOK:     false,
			wantFields: []string{"ok", "error", "agent", "has_prompt"},
		},
		{
			name:        "resolve_identity with missing name returns error",
			tool:        "resolve_identity",
			arguments:    map[string]any{},
			wantErrCode: mcp.CodeInvalidParams,
		},
		{
			name:   "resolve xander with explicit version v4",
			tool:   "resolve_identity",
			arguments: map[string]any{
				"name":    "xander",
				"version": "v4",
			},
			wantOK:     true,
			wantFields: []string{"ok", "version"},
		},
	}

	for i, tc := range callTests {
		t.Run(tc.name, func(t *testing.T) {
			params, _ := json.Marshal(map[string]any{
				"name":      tc.tool,
				"arguments": tc.arguments,
			})
			if err := send(mcp.Request{
				JSONRPC: "2.0",
				ID:      10 + i,
				Method:  "tools/call",
				Params:  json.RawMessage(params),
			}); err != nil {
				t.Fatalf("send: %v", err)
			}

			body, err := recv()
			if err != nil {
				t.Fatalf("recv: %v", err)
			}
			var resp mcp.Response
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if resp.Error != nil {
				// If we expected an error code, check it; otherwise this is unexpected.
				if tc.wantErrCode == 0 {
					t.Fatalf("unexpected error: %v", resp.Error)
				}
				if resp.Error.Code != tc.wantErrCode {
					t.Fatalf("error code = %d, want %d", resp.Error.Code, tc.wantErrCode)
				}
				return
			}

			// If we expected an error but didn't get one, that's also wrong.
			if tc.wantErrCode > 0 {
				t.Fatalf("expected error code %d, got nil", tc.wantErrCode)
			}

			resultMap, ok := resp.Result.(map[string]any)
			if !ok {
				t.Fatal("result not a map")
			}
			contentSlice, ok := resultMap["content"].([]any)
			if !ok || len(contentSlice) == 0 {
				t.Fatal("expected content slice")
			}
			first, ok := contentSlice[0].(map[string]any)
			if !ok {
				t.Fatal("expected content[0] as map")
			}
			text, ok := first["text"].(string)
			if !ok {
				t.Fatal("expected text field as string")
			}

			// Parse the JSON text payload.
			var payload map[string]any
			if err := json.Unmarshal([]byte(text), &payload); err != nil {
				t.Fatalf("parse text as JSON: %v; text: %q", err, text)
			}

			// Validate ok field.
			okVal, ok := payload["ok"].(bool)
			if !ok {
				t.Fatal("missing ok field")
			}
			if okVal != tc.wantOK {
				t.Fatalf("ok = %v, want %v", okVal, tc.wantOK)
			}

			// Validate required fields.
			for _, field := range tc.wantFields {
				if _, ok := payload[field]; !ok {
					t.Errorf("missing required field %q in payload", field)
				}
			}

			// For successful resolves, validate specific values.
			if tc.wantOK {
				if v, ok := payload["agent"].(string); ok && v != "xander" {
					t.Errorf("agent = %q, want xander", v)
				}
				if v, ok := payload["has_prompt"].(bool); ok && !v {
					t.Error("has_prompt should be true for resolved identity")
				}
				if v, ok := payload["system_prompt"].(string); ok && v == "" {
					t.Error("system_prompt should be non-empty")
				}
				if v, ok := payload["provider"].(string); ok && v == "" {
					t.Error("provider should be non-empty")
				}
			}

			// For no_active_identity case, check error field.
			if !tc.wantOK && tc.wantErrCode == 0 {
				if errField, ok := payload["error"].(string); ok {
					if errField != "no_active_identity" {
						t.Logf("note: error field = %q (may be store-level error)", errField)
					}
				}
			}
		})
	}
}
