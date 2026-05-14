//go:build xander

package xander

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/secrets"
	"github.com/silas/otoxan/pkg/stores/agentregistry"
	"github.com/silas/otoxan/pkg/stores/scopestore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRotateSelf drills the full DS-5 rotation sequence against a stub Infisical server.
//
// Acceptance criteria:
//   - prompt → generate new credential
//   - verify → validate new credential works (auth accepted, 404 on non-existent secret = ok)
//   - swap → replace the active credential in XanderClient
//   - refresh active bundles → active agents receive {op:"bundle_refresh"} notifications
//   - PhaseDone reached
//   - post-rotation RequestBundle uses the new credential
func TestRotateSelf(t *testing.T) {
	ctx := context.Background()
	mongoClient := setupMongo(t)

	// --- Stores ---
	registry, err := agentregistry.NewStore(mongoClient)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(mongoClient)
	require.NoError(t, err)

	// --- Stub Infisical server ---
	var (
		authMu   sync.Mutex
		authCount int
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/oauth":
			if r.Method != http.MethodPost {
				t.Fatalf("expected POST, got %s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if r.Form.Get("client_id") == "" || r.Form.Get("client_secret") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]any{"message": "missing credentials"})
				return
			}
			authMu.Lock()
			authCount++
			authMu.Unlock()
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "stub-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})

		case "/api/v3/secrets/raw/DB_PASSWORD":
			json.NewEncoder(w).Encode(map[string]any{
				"secret": map[string]string{"secretValue": "hunter2"},
			})

		default:
			// TestCredential probe for non-existent secret.
			// Infisical returns 404 (not auth error) for missing secrets.
			if r.URL.Path == "/api/v3/secrets/raw/__test_marker__" {
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]any{"message": "not found"})
				return
			}
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	// --- XanderClient ---
	infisical := secrets.NewClient(ts.URL, "old-admin-id", "old-admin-secret")
	xc := secrets.NewXanderClient(infisical, scopes)
	xc.WorkspaceID = "proj-123"
	xc.Environment = "dev"
	xc.DefaultTTL = 30 * time.Minute

	// --- AgentManager + AgentCreator ---
	am := NewAgentManager(registry, scopes, nil)
	ac := NewAgentCreator(registry, scopes, nil)

	// --- Seed two active agents with scope ---
	for _, name := range []string{"alice", "bob"} {
		_, err = ac.CreateAgent(ctx, name, "worker")
		require.NoError(t, err)
		_, err = scopes.Grant(ctx, name, []string{"DB_PASSWORD"})
		require.NoError(t, err)
	}

	// --- Server (for completeness; test exercises rotator directly) ---
	sockDir := t.TempDir()
	sockPath := filepath.Join(sockDir, "xander_rotate_test.sock")
	cfg := ServerConfig{
		SocketPath:   sockPath,
		Client:       xc,
		AgentManager: am,
		AgentCreator: ac,
		AuditCap:     100,
	}
	server, err := NewServer(cfg)
	require.NoError(t, err)
	go func() { _ = server.Serve() }()
	t.Cleanup(func() {
		_ = server.Shutdown(context.Background())
	})
	require.Eventually(t, func() bool {
		_, err := os.Stat(sockPath)
		return err == nil
	}, 2*time.Second, 50*time.Millisecond)

	conn, err := net.Dial("unix", sockPath)
	require.NoError(t, err)
	defer conn.Close()

	// --- Collect bundle_refresh notifications ---
	var notifiedMu sync.Mutex
	notified := map[string]bool{}

	notifier := func(ctx context.Context, agentName string, payload map[string]any) {
		notifiedMu.Lock()
		notified[agentName] = true
		notifiedMu.Unlock()
	}

	// Exercise the rotator directly with the custom notifier.
	// (IPC handler wires in a printf notifier for production observation.)
	rot := newRotator(xc, am, ac)
	rot.notifiers = append(rot.notifiers, notifier)

	// --- Execute DS-5 drill ---
	state, err := rot.RotateSelf(ctx)
	require.NoError(t, err, "rotation should not error")

	// --- Assertions ---

	// 1. Phase reaches PhaseDone
	assert.Equal(t, PhaseDone, state.Phase, "rotation should reach PhaseDone")
	assert.NotEmpty(t, state.Message, "message should describe completion")

	// 2. Both active agents received bundle_refresh notification
	notifiedMu.Lock()
	assert.True(t, notified["alice"], "alice should have received {op:\"bundle_refresh\"} notification")
	assert.True(t, notified["bob"], "bob should have received {op:\"bundle_refresh\"} notification")
	notifiedMu.Unlock()

	// 3. Post-rotation RequestBundle works with the new credential
	bundle, err := xc.RequestBundle(ctx, "alice")
	require.NoError(t, err, "RequestBundle should work after credential swap")
	assert.Contains(t, bundle.Secrets, "DB_PASSWORD")
	assert.Equal(t, "hunter2", bundle.Secrets["DB_PASSWORD"], "DB_PASSWORD should have correct value")

	// 4. IPC handler also works end-to-end
	t.Run("ipc_handler", func(t *testing.T) {
		req := Request{
			Op: OpRotateSelf,
			ID: "ipc-rotate-1",
		}
		resp := sendRecvRotate(t, conn, req)
		assert.Equal(t, "ipc-rotate-1", resp.ID)
		assert.True(t, resp.OK, "IPC handler should return ok")

		var result RotateSelfResult
		require.NoError(t, json.Unmarshal(resp.Result, &result))
		assert.Equal(t, "complete", result.Status, "IPC result status should be complete")
	})
}

// sendRecvRotate sends a Request and reads a Response over the Unix socket.
func sendRecvRotate(t *testing.T, conn net.Conn, req Request) Response {
	t.Helper()
	b, err := json.Marshal(req)
	require.NoError(t, err)
	b = append(b, '\n')
	_, err = conn.Write(b)
	require.NoError(t, err)

	// Read response
	buf := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	require.NoError(t, err)
	var resp Response
	require.NoError(t, json.Unmarshal(buf[:n], &resp))
	return resp
}
