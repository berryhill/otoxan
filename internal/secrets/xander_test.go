//go:build integration

package secrets

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/silas/otoxan/pkg/stores/scopestore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Test doubles
// ------------------------------------------------------------------

// fakeClock is a deterministic clock for tests.
type fakeClock struct {
	t time.Time
}

func (fc *fakeClock) Now() time.Time { return fc.t }
func (fc *fakeClock) Advance(d time.Duration) { fc.t = fc.t.Add(d) }

// fakeAuditor records every audit event in a slice for later inspection.
type fakeAuditor struct {
	events []AuditEvent
}

func (fa *fakeAuditor) Record(_ context.Context, ev AuditEvent) error {
	fa.events = append(fa.events, ev)
	return nil
}

// ------------------------------------------------------------------
// Mongo helper (shared with existing tests)
// ------------------------------------------------------------------

// setupMongo spins up a testcontainers MongoDB container and returns a client.
func setupMongo(t *testing.T) *mongo.Client {
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

	return client
}

// ------------------------------------------------------------------
// Existing integration tests
// ------------------------------------------------------------------

// TestXanderClient_RequestBundle_integration verifies the full flow:
//   1. Grant a scope to an agent in MongoDB.
//   2. Stand up a mock Infisical server that serves two secrets.
//   3. RequestBundle fetches both secrets and returns a non-expired bundle.
func TestXanderClient_RequestBundle_integration(t *testing.T) {
	ctx := context.Background()

	// ---- 1. MongoDB scope store ----
	mongoClient := setupMongo(t)
	scopes, err := scopestore.NewStore(mongoClient)
	require.NoError(t, err)

	agentName := "silas"
	secretPaths := []string{"DB_PASSWORD", "API_KEY"}

	_, err = scopes.Grant(ctx, agentName, secretPaths)
	require.NoError(t, err)

	// ---- 2. Mock Infisical server ----
	var tokenCalls, secretCalls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/oauth":
			tokenCalls++
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "***",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})

		case "/api/v3/secrets/raw/DB_PASSWORD":
			secretCalls++
			json.NewEncoder(w).Encode(map[string]any{
				"secret": map[string]string{"secretValue": "hunter2"},
			})

		case "/api/v3/secrets/raw/API_KEY":
			secretCalls++
			json.NewEncoder(w).Encode(map[string]any{
				"secret": map[string]string{"secretValue": "sk-abc123"},
			})

		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	// ---- 3. Xander client ----
	infisical := NewClient(ts.URL, "admin-id", "admin-secret")
	infisical.HTTPClient = &http.Client{Timeout: 2 * time.Second}

	xander := NewXanderClient(infisical, scopes)
	xander.WorkspaceID = "proj-123"
	xander.Environment = "dev"
	xander.DefaultTTL = 30 * time.Minute

	// ---- 4. Request bundle ----
	bundle, err := xander.RequestBundle(ctx, agentName)
	require.NoError(t, err)
	require.NotNil(t, bundle)

	// ---- 5. Assertions ----
	assert.Equal(t, "hunter2", bundle.Secrets["DB_PASSWORD"], "DB_PASSWORD should resolve")
	assert.Equal(t, "sk-abc123", bundle.Secrets["API_KEY"], "API_KEY should resolve")
	assert.Len(t, bundle.Secrets, 2, "bundle should contain exactly the two granted secrets")

	assert.False(t, bundle.IsExpired(), "bundle should not be expired immediately after creation")
	assert.True(t, bundle.ExpiresAt.After(time.Now().Add(29*time.Minute)), "bundle should live at least 29 min")
	assert.True(t, bundle.ExpiresAt.Before(time.Now().Add(31*time.Minute)), "bundle should expire within 31 min")

	assert.Equal(t, 1, tokenCalls, "exactly one token exchange should happen")
	assert.Equal(t, 2, secretCalls, "exactly two secret fetches should happen")
}

// TestXanderClient_RequestBundle_noScope verifies that RequestBundle returns
// an error when the agent has never been granted a scope.
func TestXanderClient_RequestBundle_noScope(t *testing.T) {
	ctx := context.Background()
	mongoClient := setupMongo(t)
	scopes, err := scopestore.NewStore(mongoClient)
	require.NoError(t, err)

	infisical := NewClient("http://example.com", "admin-id", "admin-secret")
	xander := NewXanderClient(infisical, scopes)

	_, err = xander.RequestBundle(ctx, "unknown-agent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope lookup")
}

// TestXanderClient_RequestBundle_revokedScope verifies that RequestBundle
// returns an error when the agent's scope has been revoked (soft-deleted).
func TestXanderClient_RequestBundle_revokedScope(t *testing.T) {
	ctx := context.Background()
	mongoClient := setupMongo(t)
	scopes, err := scopestore.NewStore(mongoClient)
	require.NoError(t, err)

	agentName := "silas"
	_, err = scopes.Grant(ctx, agentName, []string{"DB_PASSWORD"})
	require.NoError(t, err)

	_, err = scopes.Revoke(ctx, agentName)
	require.NoError(t, err)

	infisical := NewClient("http://example.com", "admin-id", "admin-secret")
	xander := NewXanderClient(infisical, scopes)

	_, err = xander.RequestBundle(ctx, agentName)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope lookup")
}

// ------------------------------------------------------------------
// New unit tests: Bundle TTL + audit
// ------------------------------------------------------------------

// TestBundle_ExpiresAt_is_now_plus_ttl asserts that a bundle's ExpiresAt is
// exactly now + DefaultTTL when minted under a fake clock.
func TestBundle_ExpiresAt_is_now_plus_ttl(t *testing.T) {
	ctx := context.Background()
	mongoClient := setupMongo(t)
	scopes, err := scopestore.NewStore(mongoClient)
	require.NoError(t, err)

	agentName := "silas"
	secretPaths := []string{"DB_PASSWORD"}
	_, err = scopes.Grant(ctx, agentName, secretPaths)
	require.NoError(t, err)

	// Mock Infisical server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/oauth":
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok",
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

	infisical := NewClient(ts.URL, "admin-id", "admin-secret")
	infisical.HTTPClient = &http.Client{Timeout: 2 * time.Second}

	// Fake clock pinned at a known instant.
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	fc := &fakeClock{t: now}

	xander := NewXanderClient(infisical, scopes)
	xander.WorkspaceID = "proj-123"
	xander.Environment = "dev"
	xander.DefaultTTL = 30 * time.Minute
	xander.clock = fc

	bundle, err := xander.RequestBundle(ctx, agentName)
	require.NoError(t, err)
	require.NotNil(t, bundle)

	assert.Equal(t, now.Add(30*time.Minute), bundle.ExpiresAt,
		"ExpiresAt must be exactly now + DefaultTTL")
}

// TestBundle_IsExpired_after_fake_clock_advance verifies that a bundle is not
// expired immediately, but becomes expired after the fake clock is advanced
// past its ExpiresAt.
func TestBundle_IsExpired_after_fake_clock_advance(t *testing.T) {
	ctx := context.Background()
	mongoClient := setupMongo(t)
	scopes, err := scopestore.NewStore(mongoClient)
	require.NoError(t, err)

	agentName := "silas"
	secretPaths := []string{"DB_PASSWORD"}
	_, err = scopes.Grant(ctx, agentName, secretPaths)
	require.NoError(t, err)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/oauth":
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok",
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

	infisical := NewClient(ts.URL, "admin-id", "admin-secret")
	infisical.HTTPClient = &http.Client{Timeout: 2 * time.Second}

	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	fc := &fakeClock{t: now}

	xander := NewXanderClient(infisical, scopes)
	xander.WorkspaceID = "proj-123"
	xander.Environment = "dev"
	xander.DefaultTTL = 1 * time.Hour
	xander.clock = fc

	bundle, err := xander.RequestBundle(ctx, agentName)
	require.NoError(t, err)
	require.NotNil(t, bundle)

	assert.False(t, bundle.IsExpired(), "bundle should not be expired at creation time")

	// Advance clock past the TTL.
	fc.Advance(2 * time.Hour)
	assert.True(t, bundle.IsExpired(), "bundle should be expired after clock advances past TTL")
}

// TestXanderClient_RequestBundle_audit_success verifies that a successful
// RequestBundle writes an audit event with Success=true and the correct paths.
func TestXanderClient_RequestBundle_audit_success(t *testing.T) {
	ctx := context.Background()
	mongoClient := setupMongo(t)
	scopes, err := scopestore.NewStore(mongoClient)
	require.NoError(t, err)

	agentName := "silas"
	secretPaths := []string{"DB_PASSWORD", "API_KEY"}
	_, err = scopes.Grant(ctx, agentName, secretPaths)
	require.NoError(t, err)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/oauth":
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		case "/api/v3/secrets/raw/DB_PASSWORD":
			json.NewEncoder(w).Encode(map[string]any{
				"secret": map[string]string{"secretValue": "hunter2"},
			})
		case "/api/v3/secrets/raw/API_KEY":
			json.NewEncoder(w).Encode(map[string]any{
				"secret": map[string]string{"secretValue": "sk-abc123"},
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	infisical := NewClient(ts.URL, "admin-id", "admin-secret")
	infisical.HTTPClient = &http.Client{Timeout: 2 * time.Second}

	fa := &fakeAuditor{}

	xander := NewXanderClient(infisical, scopes)
	xander.WorkspaceID = "proj-123"
	xander.Environment = "dev"
	xander.DefaultTTL = 30 * time.Minute
	xander.auditor = fa

	_, err = xander.RequestBundle(ctx, agentName)
	require.NoError(t, err)

	require.Len(t, fa.events, 1, "exactly one audit event should be recorded")
	ev := fa.events[0]
	assert.Equal(t, agentName, ev.AgentName)
	assert.True(t, ev.Success)
	assert.Equal(t, "", ev.Error)
	assert.Equal(t, secretPaths, ev.Paths)
	assert.WithinDuration(t, time.Now(), ev.RequestedAt, 5*time.Second)
}

// TestXanderClient_RequestBundle_audit_failure_noScope verifies that a failed
// RequestBundle (no scope) writes an audit event with Success=false.
func TestXanderClient_RequestBundle_audit_failure_noScope(t *testing.T) {
	ctx := context.Background()
	mongoClient := setupMongo(t)
	scopes, err := scopestore.NewStore(mongoClient)
	require.NoError(t, err)

	infisical := NewClient("http://example.com", "admin-id", "admin-secret")
	fa := &fakeAuditor{}

	xander := NewXanderClient(infisical, scopes)
	xander.auditor = fa

	_, err = xander.RequestBundle(ctx, "unknown-agent")
	require.Error(t, err)

	require.Len(t, fa.events, 1, "exactly one audit event should be recorded for the failed request")
	ev := fa.events[0]
	assert.Equal(t, "unknown-agent", ev.AgentName)
	assert.False(t, ev.Success)
	assert.Contains(t, ev.Error, "scope lookup")
	assert.Nil(t, ev.Paths)
}

// TestXanderClient_RequestBundle_audit_failure_revokedScope verifies that a
// failed RequestBundle (revoked scope) writes an audit event with Success=false.
func TestXanderClient_RequestBundle_audit_failure_revokedScope(t *testing.T) {
	ctx := context.Background()
	mongoClient := setupMongo(t)
	scopes, err := scopestore.NewStore(mongoClient)
	require.NoError(t, err)

	agentName := "silas"
	_, err = scopes.Grant(ctx, agentName, []string{"DB_PASSWORD"})
	require.NoError(t, err)
	_, err = scopes.Revoke(ctx, agentName)
	require.NoError(t, err)

	infisical := NewClient("http://example.com", "admin-id", "admin-secret")
	fa := &fakeAuditor{}

	xander := NewXanderClient(infisical, scopes)
	xander.auditor = fa

	_, err = xander.RequestBundle(ctx, agentName)
	require.Error(t, err)

	require.Len(t, fa.events, 1, "exactly one audit event should be recorded for the failed request")
	ev := fa.events[0]
	assert.Equal(t, agentName, ev.AgentName)
	assert.False(t, ev.Success)
	assert.Contains(t, ev.Error, "scope lookup")
	assert.Nil(t, ev.Paths)
}

// TestXanderClient_RequestBundle_audit_failure_secretFetch verifies that a
// failed RequestBundle (Infisical fetch error) writes an audit event with
// Success=false and the attempted paths.
func TestXanderClient_RequestBundle_audit_failure_secretFetch(t *testing.T) {
	ctx := context.Background()
	mongoClient := setupMongo(t)
	scopes, err := scopestore.NewStore(mongoClient)
	require.NoError(t, err)

	agentName := "silas"
	secretPaths := []string{"MISSING_SECRET"}
	_, err = scopes.Grant(ctx, agentName, secretPaths)
	require.NoError(t, err)

	// Infisical server that returns 404 for the secret.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/token/oauth":
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "tok",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		case "/api/v3/secrets/raw/MISSING_SECRET":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	infisical := NewClient(ts.URL, "admin-id", "admin-secret")
	infisical.HTTPClient = &http.Client{Timeout: 2 * time.Second}

	fa := &fakeAuditor{}

	xander := NewXanderClient(infisical, scopes)
	xander.WorkspaceID = "proj-123"
	xander.Environment = "dev"
	xander.auditor = fa

	_, err = xander.RequestBundle(ctx, agentName)
	require.Error(t, err)

	require.Len(t, fa.events, 1, "exactly one audit event should be recorded for the failed fetch")
	ev := fa.events[0]
	assert.Equal(t, agentName, ev.AgentName)
	assert.False(t, ev.Success)
	assert.Contains(t, ev.Error, "fetch \"MISSING_SECRET\"")
	assert.Equal(t, secretPaths, ev.Paths)
}
