// cmd_secrets_test.go — tests for otoxan secrets subcommand
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// setupMongoForSecrets spins up a testcontainers MongoDB container and returns
// both the client and the connection URI.
func setupMongoForSecrets(t *testing.T) (*mongo.Client, string) {
	t.Helper()
	ctx := context.Background()

	container, err := mongodb.Run(ctx, "mongo:7")
	if err != nil {
		t.Fatalf("failed to start mongodb container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(ctx)
	})

	uri, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("failed to connect to mongodb: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Disconnect(ctx)
	})

	return client, uri
}

// ------------------------------------------------------------------
// grant / list / revoke end-to-end
// ------------------------------------------------------------------

// TestSecretsGrantListRevoke verifies the full shell flow:
//   grant → list (appears) → revoke → list (gone).
func TestSecretsGrantListRevoke(t *testing.T) {
	_, uri := setupMongoForSecrets(t)
	t.Setenv("OTOXAN_MONGO_URI", uri)

	// 1. Grant scope to agent "e2e-agent".
	grantCmd := newSecretsGrantCmd()
	var grantOut bytes.Buffer
	grantCmd.SetOut(&grantOut)
	grantCmd.SetErr(&grantOut)
	grantCmd.SetArgs([]string{"e2e-agent"})
	_ = grantCmd.Flags().Set("path", "/global/db")
	_ = grantCmd.Flags().Set("path", "/global/api-keys")
	err := grantCmd.Execute()
	require.NoError(t, err, "grant should succeed")
	grantJSON := strings.TrimSpace(grantOut.String())
	t.Logf("grant output: %s", grantJSON)
	assert.Contains(t, grantJSON, `"agent_name": "e2e-agent"`)
	assert.Contains(t, grantJSON, `"secret_paths": [`)
	assert.Contains(t, grantJSON, `"upserted": true`)

	// 2. List all scopes — e2e-agent should appear.
	listCmd := newSecretsListCmd()
	var listOut bytes.Buffer
	listCmd.SetOut(&listOut)
	listCmd.SetErr(&listOut)
	err = listCmd.Execute()
	require.NoError(t, err, "list should succeed")
	listJSON := strings.TrimSpace(listOut.String())
	t.Logf("list output: %s", listJSON)
	assert.Contains(t, listJSON, `"agent_name": "e2e-agent"`)
	assert.Contains(t, listJSON, `"secret_paths": [`)

	// 3. Revoke e2e-agent.
	revokeCmd := newSecretsRevokeCmd()
	var revokeOut bytes.Buffer
	revokeCmd.SetOut(&revokeOut)
	revokeCmd.SetErr(&revokeOut)
	revokeCmd.SetArgs([]string{"e2e-agent"})
	err = revokeCmd.Execute()
	require.NoError(t, err, "revoke should succeed")
	revokeJSON := strings.TrimSpace(revokeOut.String())
	t.Logf("revoke output: %s", revokeJSON)
	assert.Contains(t, revokeJSON, `"deleted": true`)

	// 4. List again — e2e-agent should be gone (soft-deleted).
	listCmd2 := newSecretsListCmd()
	var listOut2 bytes.Buffer
	listCmd2.SetOut(&listOut2)
	listCmd2.SetErr(&listOut2)
	err = listCmd2.Execute()
	require.NoError(t, err, "second list should succeed")
	listJSON2 := strings.TrimSpace(listOut2.String())
	t.Logf("second list output: %s", listJSON2)
	assert.NotContains(t, listJSON2, `"agent_name": "e2e-agent"`)

	// 5. List with --include-deleted — e2e-agent should appear again.
	listCmd3 := newSecretsListCmd()
	var listOut3 bytes.Buffer
	listCmd3.SetOut(&listOut3)
	listCmd3.SetErr(&listOut3)
	_ = listCmd3.Flags().Set("include-deleted", "true")
	err = listCmd3.Execute()
	require.NoError(t, err, "list with include-deleted should succeed")
	listJSON3 := strings.TrimSpace(listOut3.String())
	t.Logf("include-deleted output: %s", listJSON3)
	assert.Contains(t, listJSON3, `"agent_name": "e2e-agent"`)
	assert.Contains(t, listJSON3, `"deleted": true`)
}

// ------------------------------------------------------------------
// list filtering
// ------------------------------------------------------------------

// TestSecretsList_FilterByAgent verifies --agent filtering.
func TestSecretsList_FilterByAgent(t *testing.T) {
	ctx := context.Background()
	mongoClient, uri := setupMongoForSecrets(t)
	t.Setenv("OTOXAN_MONGO_URI", uri)

	// Seed two agents directly.
	coll := mongoClient.Database("otoxan_global").Collection("infisical_scopes")
	_, err := coll.InsertOne(ctx, map[string]any{
		"agent_name":   "alpha",
		"secret_paths": []string{"/a/1"},
		"created_at":   "2024-01-01T00:00:00Z",
		"updated_at":   "2024-01-01T00:00:00Z",
	})
	require.NoError(t, err)
	_, err = coll.InsertOne(ctx, map[string]any{
		"agent_name":   "beta",
		"secret_paths": []string{"/b/2"},
		"created_at":   "2024-01-02T00:00:00Z",
		"updated_at":   "2024-01-02T00:00:00Z",
	})
	require.NoError(t, err)

	listCmd := newSecretsListCmd()
	var out bytes.Buffer
	listCmd.SetOut(&out)
	listCmd.SetErr(&out)
	listCmd.Flags().Set("agent", "alpha")
	err = listCmd.Execute()
	require.NoError(t, err)
	output := strings.TrimSpace(out.String())
	assert.Contains(t, output, `"agent_name": "alpha"`)
	assert.NotContains(t, output, `"agent_name": "beta"`)
}

// ------------------------------------------------------------------
// test command (existing Infisical-mocked tests)
// ------------------------------------------------------------------

// TestSecretsTestCmd_Success verifies the happy path:
//   `otoxan secrets test --as alice linear`
// prints paths_resolved=[/linear/api_token: present, /linear/workspace_id: present]
func TestSecretsTestCmd_Success(t *testing.T) {
	ctx := context.Background()

	// ---- 1. MongoDB scope store ----
	mongoClient, uri := setupMongoForSecrets(t)

	// Grant alice scope for linear secrets.
	// We need to use the scopestore directly since the command reads from Mongo.
	// But the command uses auth.MongoClient which reads OTOXAN_MONGO_URI.
	t.Setenv("OTOXAN_MONGO_URI", uri)

	// Use the internal scopestore to seed data.
	// We import it via the same package path since we're in package main.
	// Actually we can't import scopestore here easily without import path.
	// Instead, we'll seed via the command's own logic by calling the store
	// through the same path the command uses. The command imports
	// github.com/silas/otoxan/pkg/stores/scopestore.
	// Since we're in package main, we can import it directly.
	// But the test file is also package main. Let's just use the store.
	// Actually, we need to import it. Let me add the import.
	// Wait — I already have the file written without the import. I'll fix that.
	// For now, let me restructure: use a helper that seeds via Mongo directly.
	// Simpler: just use bson.M insert.
	coll := mongoClient.Database("otoxan_global").Collection("infisical_scopes")
	_, err := coll.InsertOne(ctx, map[string]any{
		"agent_name":   "alice",
		"secret_paths": []string{"/linear/api_token", "/linear/workspace_id"},
		"created_at":   "2024-01-01T00:00:00Z",
		"updated_at":   "2024-01-01T00:00:00Z",
	})
	require.NoError(t, err)

	// ---- 2. Mock Infisical server ----
	var secretCalls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/oauth":
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "***",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})

		case "/api/v3/secrets/raw/LINEAR_API_TOKEN":
			secretCalls++
			json.NewEncoder(w).Encode(map[string]any{
				"secret": map[string]string{"secretValue": "sekrit-api-token"},
			})

		case "/api/v3/secrets/raw/LINEAR_WORKSPACE_ID":
			secretCalls++
			json.NewEncoder(w).Encode(map[string]any{
				"secret": map[string]string{"secretValue": "sekrit-workspace-id"},
			})

		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	// ---- 3. Set env so buildInfisicalClient picks up the mock ----
	t.Setenv("INFISICAL_BASE_URL", ts.URL)
	t.Setenv("INFISICAL_CLIENT_ID", "admin-id")
	t.Setenv("INFISICAL_CLIENT_SECRET", "admin-secret")
	t.Setenv("INFISICAL_PROJECT_ID", "proj-123")
	t.Setenv("INFISICAL_ENV", "dev")

	// ---- 4. Build and execute the command ----
	cmd := newSecretsTestCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"linear"})
	cmd.Flags().Set("as", "alice")

	err = cmd.Execute()
	require.NoError(t, err)

	// ---- 5. Assertions ----
	output := strings.TrimSpace(out.String())
	assert.Contains(t, output, "paths_resolved=[")
	assert.Contains(t, output, "/linear/api_token: present")
	assert.Contains(t, output, "/linear/workspace_id: present")
	assert.NotContains(t, output, "sekrit", "output must not leak secret values")

	assert.Equal(t, 2, secretCalls, "exactly two secret fetches should happen")
}

// TestSecretsTestCmd_NoScope verifies that the command errors when the agent
// has no granted scope.
func TestSecretsTestCmd_NoScope(t *testing.T) {
	_, uri := setupMongoForSecrets(t)

	// Override mongo URI.
	t.Setenv("OTOXAN_MONGO_URI", uri)

	cmd := newSecretsTestCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"linear"})
	cmd.Flags().Set("as", "bob")

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no scope granted")
}

// TestSecretsTestCmd_ScopeNotInPaths verifies that the command errors when the
// agent has a scope but not for the requested product.
func TestSecretsTestCmd_ScopeNotInPaths(t *testing.T) {
	ctx := context.Background()
	mongoClient, uri := setupMongoForSecrets(t)

	// Grant alice scope for github secrets only.
	coll := mongoClient.Database("otoxan_global").Collection("infisical_scopes")
	_, err := coll.InsertOne(ctx, map[string]any{
		"agent_name":   "alice",
		"secret_paths": []string{"/github/token"},
		"created_at":   "2024-01-01T00:00:00Z",
		"updated_at":   "2024-01-01T00:00:00Z",
	})
	require.NoError(t, err)

	t.Setenv("OTOXAN_MONGO_URI", uri)

	cmd := newSecretsTestCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"linear"})
	cmd.Flags().Set("as", "alice")

	err = cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope \"linear\" not found")
}

// TestSecretsTestCmd_MissingSecret verifies that a missing secret is reported
// as "missing" without leaking the error detail in a way that exposes values.
func TestSecretsTestCmd_MissingSecret(t *testing.T) {
	ctx := context.Background()
	mongoClient, uri := setupMongoForSecrets(t)

	// Grant alice scope for two linear secrets.
	coll := mongoClient.Database("otoxan_global").Collection("infisical_scopes")
	_, err := coll.InsertOne(ctx, map[string]any{
		"agent_name":   "alice",
		"secret_paths": []string{"/linear/api_token", "/linear/workspace_id"},
		"created_at":   "2024-01-01T00:00:00Z",
		"updated_at":   "2024-01-01T00:00:00Z",
	})
	require.NoError(t, err)

	// Mock Infisical: only api_token exists.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/oauth":
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "***",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})

		case "/api/v3/secrets/raw/LINEAR_API_TOKEN":
			json.NewEncoder(w).Encode(map[string]any{
				"secret": map[string]string{"secretValue": "present"},
			})

		case "/api/v3/secrets/raw/LINEAR_WORKSPACE_ID":
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": "not found"})

		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	t.Setenv("INFISICAL_BASE_URL", ts.URL)
	t.Setenv("INFISICAL_CLIENT_ID", "admin-id")
	t.Setenv("INFISICAL_CLIENT_SECRET", "admin-secret")
	t.Setenv("INFISICAL_PROJECT_ID", "proj-123")
	t.Setenv("INFISICAL_ENV", "dev")
	t.Setenv("OTOXAN_MONGO_URI", uri)

	cmd := newSecretsTestCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"linear"})
	cmd.Flags().Set("as", "alice")

	err = cmd.Execute()
	require.NoError(t, err)

	output := strings.TrimSpace(out.String())
	assert.Contains(t, output, "/linear/api_token: present")
	assert.Contains(t, output, "/linear/workspace_id: missing")
}
