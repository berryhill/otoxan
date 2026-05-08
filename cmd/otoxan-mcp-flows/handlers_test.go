package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/mcp"
	"github.com/silas/otoxan/internal/store/flows"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func setupTestStores(t *testing.T) (*flows.FlowStore, *mongo.Collection, func()) {
	t.Helper()
	ctx := context.Background()

	ctr, err := mongodb.Run(ctx, "mongo:7")
	require.NoError(t, err)

	uri, err := ctr.ConnectionString(ctx)
	require.NoError(t, err)

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	require.NoError(t, err)

	db := client.Database("test")
	flowColl := db.Collection("flows")
	flowStore := flows.NewFlowStore(flowColl)
	templateColl := db.Collection("flow_templates")

	cleanup := func() {
		_ = client.Disconnect(ctx)
		_ = ctr.Terminate(ctx)
	}
	return flowStore, templateColl, cleanup
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func makeRequest(method string, params any) []byte {
	return mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
}

func callTool(t *testing.T, srv *mcp.Server, toolName string, args any) map[string]any {
	t.Helper()
	ctx := context.Background()
	inR, inW := io.Pipe()
	out := &bytes.Buffer{}

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, inR, out)
	}()

	params := map[string]any{
		"name":      toolName,
		"arguments": args,
	}
	req := makeRequest("tools/call", params)
	if err := mcp.WriteFramed(inW, req); err != nil {
		t.Fatalf("write init: %v", err)
	}

	shutdown := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "shutdown",
	})
	if err := mcp.WriteFramed(inW, shutdown); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}
	_ = inW.Close()

	select {
	case err := <-done:
		if err != nil && err != io.EOF && !strings.Contains(err.Error(), "closed") {
			t.Logf("serve returned: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for serve")
	}

	respBody, err := mcp.ReadFramed(bytes.NewReader(out.Bytes()))
	require.NoError(t, err)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(respBody, &resp))
	return resp
}

func seedTemplate(ctx context.Context, t *testing.T, coll *mongo.Collection, name string, steps []bson.M) {
	t.Helper()
	_, err := coll.InsertOne(ctx, bson.M{
		"name":        name,
		"description": name + " description",
		"steps":       steps,
	})
	require.NoError(t, err)
}

func TestGetFlow(t *testing.T) {
	store, _, cleanup := setupTestStores(t)
	defer cleanup()

	ctx := context.Background()
	_, err := store.Create(ctx, &flows.Flow{FlowID: "f1", Name: "Test Flow", Status: flows.StatusDraft})
	require.NoError(t, err)

	srv := mcp.New("test", "0.0.1")
	registerTools(srv, store, nil)

	resp := callTool(t, srv, "get_flow", map[string]any{"flow_id": "f1"})
	require.Nil(t, resp["error"])
	result := resp["result"].(map[string]any)
	content := result["content"].([]any)
	require.Len(t, content, 1)
}

func TestStartFlow(t *testing.T) {
	store, tmplColl, cleanup := setupTestStores(t)
	defer cleanup()

	ctx := context.Background()
	seedTemplate(ctx, t, tmplColl, "onboarding", []bson.M{
		{"name": "Welcome", "type": "action", "config": bson.M{"msg": "hello"}},
		{"name": "Verify", "type": "decision", "config": bson.M{"check": true}},
	})

	srv := mcp.New("test", "0.0.1")
	registerTools(srv, store, tmplColl)

	resp := callTool(t, srv, "start_flow", map[string]any{
		"template": "onboarding",
		"context":  map[string]any{"user": "alice"},
	})
	require.Nil(t, resp["error"])

	// Verify flow exists in store.
	var found bool
	all, err := store.List(ctx, flows.ListOptions{})
	require.NoError(t, err)
	for _, f := range all {
		if f.Name == "onboarding" {
			found = true
			require.Len(t, f.Steps, 2)
			require.Equal(t, flows.StatusActive, f.Status)
		}
	}
	require.True(t, found, "expected flow named onboarding to exist")
}

func TestStartFlowUnknownTemplate(t *testing.T) {
	store, tmplColl, cleanup := setupTestStores(t)
	defer cleanup()

	srv := mcp.New("test", "0.0.1")
	registerTools(srv, store, tmplColl)

	resp := callTool(t, srv, "start_flow", map[string]any{"template": "missing"})
	require.NotNil(t, resp["error"])
	errMap := resp["error"].(map[string]any)
	require.Equal(t, float64(mcp.CodeInvalidParams), errMap["code"])
	require.Contains(t, errMap["message"], "unknown template")
}

func TestAdvanceFlow(t *testing.T) {
	store, tmplColl, cleanup := setupTestStores(t)
	defer cleanup()

	ctx := context.Background()
	seedTemplate(ctx, t, tmplColl, "simple", []bson.M{
		{"name": "Step1", "type": "action"},
		{"name": "Step2", "type": "action"},
	})

	srv := mcp.New("test", "0.0.1")
	registerTools(srv, store, tmplColl)

	// Start flow.
	resp := callTool(t, srv, "start_flow", map[string]any{"template": "simple"})
	require.Nil(t, resp["error"])

	// Find the created flow ID.
	all, err := store.List(ctx, flows.ListOptions{})
	require.NoError(t, err)
	require.Len(t, all, 1)
	flowID := all[0].FlowID

	// Advance once.
	resp = callTool(t, srv, "advance_flow", map[string]any{"flow_id": flowID})
	require.Nil(t, resp["error"])
}

func TestAdvanceFlowTerminal(t *testing.T) {
	store, tmplColl, cleanup := setupTestStores(t)
	defer cleanup()

	ctx := context.Background()
	seedTemplate(ctx, t, tmplColl, "one_step", []bson.M{
		{"name": "Only", "type": "end"},
	})

	srv := mcp.New("test", "0.0.1")
	registerTools(srv, store, tmplColl)

	resp := callTool(t, srv, "start_flow", map[string]any{"template": "one_step"})
	require.Nil(t, resp["error"])

	all, err := store.List(ctx, flows.ListOptions{})
	require.NoError(t, err)
	require.Len(t, all, 1)
	flowID := all[0].FlowID

	// Single-step flow is already at terminal step; first advance should error.
	resp = callTool(t, srv, "advance_flow", map[string]any{"flow_id": flowID})
	require.NotNil(t, resp["error"])
	errMap := resp["error"].(map[string]any)
	require.Equal(t, float64(mcp.CodeInvalidParams), errMap["code"])
	require.Contains(t, errMap["message"], "terminal")
}

func TestListFlows(t *testing.T) {
	store, _, cleanup := setupTestStores(t)
	defer cleanup()

	ctx := context.Background()
	_, err := store.Create(ctx, &flows.Flow{FlowID: "f2", Name: "A", Status: flows.StatusDraft})
	require.NoError(t, err)
	_, err = store.Create(ctx, &flows.Flow{FlowID: "f3", Name: "B", Status: flows.StatusActive})
	require.NoError(t, err)

	srv := mcp.New("test", "0.0.1")
	registerTools(srv, store, nil)

	resp := callTool(t, srv, "list_flows", map[string]any{"status": []string{"DRAFT"}})
	require.Nil(t, resp["error"])
}

func TestToolsList(t *testing.T) {
	store, tmplColl, cleanup := setupTestStores(t)
	defer cleanup()

	srv := mcp.New("test", "0.0.1")
	registerTools(srv, store, tmplColl)

	ctx := context.Background()
	inR, inW := io.Pipe()
	out := &bytes.Buffer{}

	done := make(chan error, 1)
	go func() {
		done <- srv.Serve(ctx, inR, out)
	}()

	req := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	})
	if err := mcp.WriteFramed(inW, req); err != nil {
		t.Fatalf("write: %v", err)
	}
	shutdown := mustJSON(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "shutdown",
	})
	if err := mcp.WriteFramed(inW, shutdown); err != nil {
		t.Fatalf("write shutdown: %v", err)
	}
	_ = inW.Close()

	select {
	case err := <-done:
		if err != nil && err != io.EOF && !strings.Contains(err.Error(), "closed") {
			t.Logf("serve returned: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}

	respBody, err := mcp.ReadFramed(bytes.NewReader(out.Bytes()))
	require.NoError(t, err)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(respBody, &resp))
	require.Nil(t, resp["error"])
	result := resp["result"].(map[string]any)
	tools := result["tools"].([]any)
	require.Len(t, tools, 4)

	names := make([]string, len(tools))
	for i, tt := range tools {
		tool := tt.(map[string]any)
		names[i] = tool["name"].(string)
	}
	require.ElementsMatch(t, []string{"get_flow", "start_flow", "advance_flow", "list_flows"}, names)
}
