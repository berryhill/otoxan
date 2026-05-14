//go:build xander

package xander

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/secrets"
	"github.com/silas/otoxan/pkg/stores/agentregistry"
	"github.com/silas/otoxan/pkg/stores/scopestore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ------------------------------------------------------------------
// TestServerOpDispatch — integration test for request_bundle, health, audit_tail
// ------------------------------------------------------------------

// TestServerOpDispatch verifies that a fake client can send each op over the
// Unix socket and receives the expected response shape.
func TestServerOpDispatch(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	// --- Real stores ---
	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	// --- Mock Infisical server ---
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/oauth":
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "***",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		case "/api/v3/secrets/raw/DB_PASSWORD":
			json.NewEncoder(w).Encode(map[string]any{
				"secret": map[string]string{"secretValue": "hunter2"},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	// --- XanderClient wired to mock Infisical ---
	infisical := secrets.NewClient(ts.URL, "admin-id", "admin-secret")
	infisical.HTTPClient = &http.Client{Timeout: 2 * time.Second}
	xc := secrets.NewXanderClient(infisical, scopes)
	xc.WorkspaceID = "proj-123"
	xc.Environment = "dev"
	xc.DefaultTTL = 30 * time.Minute

	// --- Agent manager + creator ---
	am := NewAgentManager(registry, scopes, nil)
	ac := NewAgentCreator(registry, scopes, nil)

	// --- Seed an active agent with scope ---
	_, err = ac.CreateAgent(ctx, "alice", "worker")
	require.NoError(t, err)
	_, err = scopes.Grant(ctx, "alice", []string{"DB_PASSWORD"})
	require.NoError(t, err)

	// --- Server on temp socket ---
	sockDir := t.TempDir()
	sockPath := filepath.Join(sockDir, "xander_test.sock")

	cfg := ServerConfig{
		SocketPath:   sockPath,
		Client:       xc,
		AgentManager: am,
		AgentCreator: ac,
		AuditCap:     100,
	}
	server, err := NewServer(cfg)
	require.NoError(t, err)

	// Start server in background.
	go func() {
		_ = server.Serve()
	}()
	t.Cleanup(func() {
		_ = server.Shutdown(context.Background())
		_ = os.RemoveAll(sockPath)
	})

	// Wait for socket to exist.
	require.Eventually(t, func() bool {
		_, err := os.Stat(sockPath)
		return err == nil
	}, 2*time.Second, 50*time.Millisecond)

	// Dial as same UID (test process).
	conn, err := net.Dial("unix", sockPath)
	require.NoError(t, err)
	defer conn.Close()

	t.Run("request_bundle_ok", func(t *testing.T) {
		req := Request{
			Op:      OpRequestBundle,
			ID:      "req-1",
			Payload: mustJSON(RequestBundlePayload{AgentName: "alice"}),
		}
		resp := sendRecv(t, conn, req)
		assert.Equal(t, "req-1", resp.ID)
		assert.True(t, resp.OK, "expected OK, got error: %s", resp.Error)
		assert.NotEmpty(t, resp.Result)

		var bundle secrets.Bundle
		require.NoError(t, json.Unmarshal(resp.Result, &bundle))
		assert.Contains(t, bundle.Secrets, "DB_PASSWORD")
		assert.Equal(t, "hunter2", bundle.Secrets["DB_PASSWORD"])
		assert.False(t, bundle.IsExpired())
	})

	t.Run("request_bundle_disabled_agent", func(t *testing.T) {
		// Disable alice first.
		require.NoError(t, am.DisableAgent(ctx, "alice"))

		req := Request{
			Op:      OpRequestBundle,
			ID:      "req-2",
			Payload: mustJSON(RequestBundlePayload{AgentName: "alice"}),
		}
		resp := sendRecv(t, conn, req)
		assert.Equal(t, "req-2", resp.ID)
		assert.False(t, resp.OK)
		assert.Contains(t, resp.Error, "disabled")

		// Re-enable for other tests.
		_, err := am.UpgradeAgent(ctx, "alice", "worker")
		require.NoError(t, err)
		_, err = scopes.Grant(ctx, "alice", []string{"DB_PASSWORD"})
		require.NoError(t, err)
	})

	t.Run("health", func(t *testing.T) {
		req := Request{
			Op: OpHealth,
			ID: "req-3",
		}
		resp := sendRecv(t, conn, req)
		assert.Equal(t, "req-3", resp.ID)
		assert.True(t, resp.OK)

		var hr HealthResult
		require.NoError(t, json.Unmarshal(resp.Result, &hr))
		assert.Equal(t, "ok", hr.Status)
		assert.NotEmpty(t, hr.Version)
		assert.GreaterOrEqual(t, hr.UptimeSec, int64(0))
	})

	t.Run("audit_tail", func(t *testing.T) {
		// First, trigger a bundle request so there's an audit event in the ring.
		_ = sendRecv(t, conn, Request{
			Op:      OpRequestBundle,
			ID:      "req-audit-seed",
			Payload: mustJSON(RequestBundlePayload{AgentName: "alice"}),
		})

		req := Request{
			Op:      OpAuditTail,
			ID:      "req-4",
			Payload: mustJSON(AuditTailPayload{AgentName: "alice", Limit: 10}),
		}
		resp := sendRecv(t, conn, req)
		assert.Equal(t, "req-4", resp.ID)
		assert.True(t, resp.OK)

		var atr AuditTailResult
		require.NoError(t, json.Unmarshal(resp.Result, &atr))
		assert.NotEmpty(t, atr.Events)
		found := false
		for _, ev := range atr.Events {
			if ev.AgentName == "alice" {
				found = true
				break
			}
		}
		assert.True(t, found, "expected at least one audit event for alice")
	})

	t.Run("unknown_op", func(t *testing.T) {
		req := Request{
			Op: "bogus",
			ID: "req-5",
		}
		resp := sendRecv(t, conn, req)
		assert.Equal(t, "req-5", resp.ID)
		assert.False(t, resp.OK)
		assert.Contains(t, resp.Error, "unknown op")
	})
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

func mustJSON(v interface{}) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return json.RawMessage(b)
}

func sendRecv(t *testing.T, conn net.Conn, req Request) Response {
	t.Helper()
	b, err := json.Marshal(req)
	require.NoError(t, err)
	b = append(b, '\n')
	_, err = conn.Write(b)
	require.NoError(t, err)

	br := bufio.NewReader(conn)
	line, err := br.ReadBytes('\n')
	require.NoError(t, err)

	var resp Response
	require.NoError(t, json.Unmarshal(line, &resp))
	return resp
}
