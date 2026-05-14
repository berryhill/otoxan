//go:build xander

package xander

import (
	"context"
	"fmt"
	"testing"

	"github.com/silas/otoxan/internal/secrets"
	"github.com/silas/otoxan/pkg/stores/agentregistry"
	"github.com/silas/otoxan/pkg/stores/scopestore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Mongo helper (shared with create_agent_test.go)
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

// fakeAuditor records every audit event in a slice for later inspection.
type fakeAuditor struct {
	events []secrets.AuditEvent
	fail   bool
}

func (fa *fakeAuditor) Record(_ context.Context, ev secrets.AuditEvent) error {
	if fa.fail {
		return fmt.Errorf("audit storage is down")
	}
	fa.events = append(fa.events, ev)
	return nil
}

// failingScopeStore wraps a real scopestore.Store and fails after N calls.
type failingScopeStore struct {
	*scopestore.Store
	failAfter int
	calls     int
}

func (f *failingScopeStore) Grant(ctx context.Context, agentName string, secretPaths []string) (*mongo.UpdateResult, error) {
	f.calls++
	if f.calls > f.failAfter {
		return nil, fmt.Errorf("injected scope grant failure")
	}
	return f.Store.Grant(ctx, agentName, secretPaths)
}

func (f *failingScopeStore) Get(ctx context.Context, agentName string) (*scopestore.ScopeDoc, error) {
	return f.Store.Get(ctx, agentName)
}

func (f *failingScopeStore) Revoke(ctx context.Context, agentName string) (*mongo.UpdateResult, error) {
	return f.Store.Revoke(ctx, agentName)
}

// failingScopeStoreRevoke wraps a real scopestore.Store and can fail on Revoke.
type failingScopeStoreRevoke struct {
	*scopestore.Store
	failRevoke bool
}

func (f *failingScopeStoreRevoke) Grant(ctx context.Context, agentName string, secretPaths []string) (*mongo.UpdateResult, error) {
	return f.Store.Grant(ctx, agentName, secretPaths)
}

func (f *failingScopeStoreRevoke) Get(ctx context.Context, agentName string) (*scopestore.ScopeDoc, error) {
	return f.Store.Get(ctx, agentName)
}

func (f *failingScopeStoreRevoke) Revoke(ctx context.Context, agentName string) (*mongo.UpdateResult, error) {
	if f.failRevoke {
		return nil, assert.AnError
	}
	return f.Store.Revoke(ctx, agentName)
}

// ------------------------------------------------------------------
// CreateAgent tests
// ------------------------------------------------------------------

// TestCreateAgent_HappyPath verifies the full atomic flow:
//   1. Creates agent registry doc.
//   2. Creates per-agent DB with __init collection.
//   3. Grants default scope.
//   4. Writes audit event.
func TestCreateAgent_HappyPath(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)

	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	fa := &fakeAuditor{}
	creator := NewAgentCreator(registry, scopes, fa)

	// Execute
	res, err := creator.CreateAgent(ctx, "test-agent", "backend")
	require.NoError(t, err)
	require.NotNil(t, res)

	// --- Verify registry doc ---
	agent, err := registry.Get(ctx, "test-agent")
	require.NoError(t, err)
	require.NotNil(t, agent)
	assert.Equal(t, "test-agent", agent.Name)
	assert.Equal(t, "backend", agent.Role)
	assert.Equal(t, "otoxan_agent_test-agent", agent.DBName)
	assert.Equal(t, agentregistry.AgentStatusActive, agent.Status)

	// --- Verify per-agent DB collections ---
	agentDB := client.Database(agent.DBName)
	colls, err := agentDB.ListCollectionNames(ctx, bson.M{})
	require.NoError(t, err)
	assert.Contains(t, colls, "__init")

	// --- Verify default scope ---
	scopeDoc, err := scopes.Get(ctx, "test-agent")
	require.NoError(t, err)
	require.NotNil(t, scopeDoc)
	assert.Equal(t, "test-agent", scopeDoc.AgentName)
	assert.Equal(t, DefaultSecretPaths, scopeDoc.SecretPaths)

	// --- Verify audit event ---
	require.Len(t, fa.events, 1)
	ev := fa.events[0]
	assert.Equal(t, "test-agent", ev.AgentName)
	assert.True(t, ev.Success)
	assert.Equal(t, "", ev.Error)
}

// ------------------------------------------------------------------
// Rollback integration test
// ------------------------------------------------------------------

// TestCreateAgent_Rollback_OnScopeGrantFailure verifies that when the scope
// grant step fails, the registry document is rolled back (hard-deleted) so
// no partial agent remains.
func TestCreateAgent_Rollback_OnScopeGrantFailure(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)

	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	fa := &fakeAuditor{}

	// Wrap the real scope store so the first Grant call fails.
	failingScopes := &failingScopeStore{Store: scopes, failAfter: 0}

	creator := NewAgentCreator(registry, failingScopes, fa)

	// Execute — should fail at scope grant.
	_, err = creator.CreateAgent(ctx, "rollback-agent", "worker")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope grant")

	// --- Verify no agent left in registry ---
	_, err = registry.Get(ctx, "rollback-agent")
	assert.Equal(t, mongo.ErrNoDocuments, err)

	// --- Verify no scope left behind ---
	_, err = scopes.Get(ctx, "rollback-agent")
	assert.Equal(t, mongo.ErrNoDocuments, err)

	// --- Verify audit event recorded the failure ---
	require.Len(t, fa.events, 1)
	ev := fa.events[0]
	assert.Equal(t, "rollback-agent", ev.AgentName)
	assert.False(t, ev.Success)
	assert.Contains(t, ev.Error, "scope grant")
}

// ------------------------------------------------------------------
// Input validation tests
// ------------------------------------------------------------------

func TestCreateAgent_Validation_EmptyName(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	creator := NewAgentCreator(registry, scopes, nil)
	_, err = creator.CreateAgent(ctx, "", "backend")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent name is required")
}

func TestCreateAgent_Validation_EmptyRole(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	creator := NewAgentCreator(registry, scopes, nil)
	_, err = creator.CreateAgent(ctx, "some-agent", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "role is required")
}

// ------------------------------------------------------------------
// Idempotency / duplicate test
// ------------------------------------------------------------------

// TestCreateAgent_DuplicateName verifies that creating the same agent twice
// fails with a duplicate error, and the second failure does NOT roll back
// the first agent.
func TestCreateAgent_DuplicateName(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	creator := NewAgentCreator(registry, scopes, nil)

	// First creation succeeds.
	res1, err := creator.CreateAgent(ctx, "dup-agent", "backend")
	require.NoError(t, err)
	require.NotNil(t, res1)

	// Second creation fails.
	_, err = creator.CreateAgent(ctx, "dup-agent", "backend")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "registry register")

	// First agent still exists.
	agent, err := registry.Get(ctx, "dup-agent")
	require.NoError(t, err)
	assert.Equal(t, "dup-agent", agent.Name)

	// Scope still exists.
	scopeDoc, err := scopes.Get(ctx, "dup-agent")
	require.NoError(t, err)
	assert.Equal(t, "dup-agent", scopeDoc.AgentName)
}

// ------------------------------------------------------------------
// Audit best-effort test
// ------------------------------------------------------------------

// TestCreateAgent_AuditBestEffort verifies that an audit failure does NOT
// cause the agent creation to fail or roll back.
func TestCreateAgent_AuditBestEffort(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	// failingAuditor always returns an error.
	fa := &fakeAuditor{fail: true}
	creator := NewAgentCreator(registry, scopes, fa)

	res, err := creator.CreateAgent(ctx, "audit-fail-agent", "backend")
	require.NoError(t, err)
	require.NotNil(t, res)

	// Agent exists despite audit failure.
	agent, err := registry.Get(ctx, "audit-fail-agent")
	require.NoError(t, err)
	assert.Equal(t, "audit-fail-agent", agent.Name)
}

// ------------------------------------------------------------------
// DisableAgent — unit + integration tests
// ------------------------------------------------------------------

// TestDisableAgent_HappyPath verifies that DisableAgent sets status INACTIVE
// and revokes the agent's scope.
func TestDisableAgent_HappyPath(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	// Create an agent first.
	creator := NewAgentCreator(registry, scopes, nil)
	_, err = creator.CreateAgent(ctx, "disable-me", "worker")
	require.NoError(t, err)

	// Grant extra scope.
	_, err = scopes.Grant(ctx, "disable-me", []string{"/global/*", "/agent/self/db"})
	require.NoError(t, err)

	fa := &fakeAuditor{}
	mgr := NewAgentManager(registry, scopes, fa)

	// Disable.
	err = mgr.DisableAgent(ctx, "disable-me")
	require.NoError(t, err)

	// Verify status is INACTIVE.
	agent, err := registry.Get(ctx, "disable-me")
	require.NoError(t, err)
	assert.Equal(t, agentregistry.AgentStatusInactive, agent.Status)

	// Verify scope is revoked (soft-deleted).
	_, err = scopes.Get(ctx, "disable-me")
	assert.Equal(t, mongo.ErrNoDocuments, err)

	// Verify audit.
	require.Len(t, fa.events, 1)
	assert.Equal(t, "disable-me", fa.events[0].AgentName)
	assert.True(t, fa.events[0].Success)
}

// TestDisableAgent_NotFound verifies that disabling a non-existent agent fails.
func TestDisableAgent_NotFound(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	fa := &fakeAuditor{}
	mgr := NewAgentManager(registry, scopes, fa)

	err = mgr.DisableAgent(ctx, "ghost-agent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	require.Len(t, fa.events, 1)
	assert.False(t, fa.events[0].Success)
	assert.Contains(t, fa.events[0].Error, "agent not found")
}

// TestDisableAgent_EmptyName verifies input validation.
func TestDisableAgent_EmptyName(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	mgr := NewAgentManager(registry, scopes, nil)
	err = mgr.DisableAgent(ctx, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent name is required")
}

// TestDisableAgent_RollbackScopeRevokeFailure verifies that when scope revoke
// fails, the agent status remains INACTIVE (no rollback — partial disable is
// acceptable because the agent is already unusable without scope).
func TestDisableAgent_RollbackScopeRevokeFailure(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	creator := NewAgentCreator(registry, scopes, nil)
	_, err = creator.CreateAgent(ctx, "disable-fail-revoke", "worker")
	require.NoError(t, err)

	failingScopes := &failingScopeStoreRevoke{Store: scopes, failRevoke: true}
	fa := &fakeAuditor{}
	mgr := NewAgentManager(registry, failingScopes, fa)

	err = mgr.DisableAgent(ctx, "disable-fail-revoke")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope revoke")

	// Status was still updated to INACTIVE.
	agent, err := registry.Get(ctx, "disable-fail-revoke")
	require.NoError(t, err)
	assert.Equal(t, agentregistry.AgentStatusInactive, agent.Status)
}

// ------------------------------------------------------------------
// UpgradeAgent — unit + integration tests
// ------------------------------------------------------------------

// TestUpgradeAgent_HappyPath verifies that UpgradeAgent changes the role.
func TestUpgradeAgent_HappyPath(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	creator := NewAgentCreator(registry, scopes, nil)
	_, err = creator.CreateAgent(ctx, "upgrade-me", "worker")
	require.NoError(t, err)

	fa := &fakeAuditor{}
	mgr := NewAgentManager(registry, scopes, fa)

	res, err := mgr.UpgradeAgent(ctx, "upgrade-me", "backend")
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Equal(t, "upgrade-me", res.AgentName)
	assert.Equal(t, "worker", res.OldRole)
	assert.Equal(t, "backend", res.NewRole)
	assert.Equal(t, agentregistry.AgentStatusActive, res.Status)

	// Verify in DB.
	agent, err := registry.Get(ctx, "upgrade-me")
	require.NoError(t, err)
	assert.Equal(t, "backend", agent.Role)
	assert.Equal(t, agentregistry.AgentStatusActive, agent.Status)

	// Audit.
	require.Len(t, fa.events, 1)
	assert.True(t, fa.events[0].Success)
}

// TestUpgradeAgent_ReactivatesInactiveAgent verifies that upgrading an
// INACTIVE agent re-activates it.
func TestUpgradeAgent_ReactivatesInactiveAgent(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	creator := NewAgentCreator(registry, scopes, nil)
	_, err = creator.CreateAgent(ctx, "reactivate-me", "worker")
	require.NoError(t, err)

	// Disable first.
	mgr := NewAgentManager(registry, scopes, nil)
	require.NoError(t, mgr.DisableAgent(ctx, "reactivate-me"))

	fa := &fakeAuditor{}
	mgr2 := NewAgentManager(registry, scopes, fa)
	res, err := mgr2.UpgradeAgent(ctx, "reactivate-me", "backend")
	require.NoError(t, err)
	assert.Equal(t, agentregistry.AgentStatusActive, res.Status)

	agent, err := registry.Get(ctx, "reactivate-me")
	require.NoError(t, err)
	assert.Equal(t, agentregistry.AgentStatusActive, agent.Status)
}

// TestUpgradeAgent_NotFound verifies error for missing agent.
func TestUpgradeAgent_NotFound(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	fa := &fakeAuditor{}
	mgr := NewAgentManager(registry, scopes, fa)

	_, err = mgr.UpgradeAgent(ctx, "ghost", "backend")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	require.Len(t, fa.events, 1)
	assert.False(t, fa.events[0].Success)
}

// TestUpgradeAgent_EmptyInputs verifies input validation.
func TestUpgradeAgent_EmptyInputs(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	mgr := NewAgentManager(registry, scopes, nil)

	_, err = mgr.UpgradeAgent(ctx, "", "backend")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent name is required")

	_, err = mgr.UpgradeAgent(ctx, "some", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "new role is required")
}

// ------------------------------------------------------------------
// GrantScope — unit + integration tests
// ------------------------------------------------------------------

// TestGrantScope_HappyPath verifies that GrantScope assigns paths to an agent.
func TestGrantScope_HappyPath(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	creator := NewAgentCreator(registry, scopes, nil)
	_, err = creator.CreateAgent(ctx, "grant-me", "worker")
	require.NoError(t, err)

	fa := &fakeAuditor{}
	mgr := NewAgentManager(registry, scopes, fa)

	res, err := mgr.GrantScope(ctx, "grant-me", []string{"/global/db", "/teams/dev"})
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.Equal(t, "grant-me", res.AgentName)
	assert.Equal(t, []string{"/global/db", "/teams/dev"}, res.SecretPaths)

	// Verify in DB.
	scopeDoc, err := scopes.Get(ctx, "grant-me")
	require.NoError(t, err)
	assert.Equal(t, []string{"/global/db", "/teams/dev"}, scopeDoc.SecretPaths)

	// Audit.
	require.Len(t, fa.events, 1)
	assert.True(t, fa.events[0].Success)
}

// TestGrantScope_ReplaceExisting verifies that GrantScope replaces prior paths.
func TestGrantScope_ReplaceExisting(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	creator := NewAgentCreator(registry, scopes, nil)
	_, err = creator.CreateAgent(ctx, "replace-me", "worker")
	require.NoError(t, err)

	mgr := NewAgentManager(registry, scopes, nil)
	_, err = mgr.GrantScope(ctx, "replace-me", []string{"/old/path"})
	require.NoError(t, err)

	_, err = mgr.GrantScope(ctx, "replace-me", []string{"/new/path"})
	require.NoError(t, err)

	scopeDoc, err := scopes.Get(ctx, "replace-me")
	require.NoError(t, err)
	assert.Equal(t, []string{"/new/path"}, scopeDoc.SecretPaths)
}

// TestGrantScope_RejectedForInactiveAgent verifies that granting scope to an
// INACTIVE agent fails.
func TestGrantScope_RejectedForInactiveAgent(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	creator := NewAgentCreator(registry, scopes, nil)
	_, err = creator.CreateAgent(ctx, "inactive-grant", "worker")
	require.NoError(t, err)

	// Disable.
	mgr := NewAgentManager(registry, scopes, nil)
	require.NoError(t, mgr.DisableAgent(ctx, "inactive-grant"))

	fa := &fakeAuditor{}
	mgr2 := NewAgentManager(registry, scopes, fa)
	_, err = mgr2.GrantScope(ctx, "inactive-grant", []string{"/global/db"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not active")

	require.Len(t, fa.events, 1)
	assert.False(t, fa.events[0].Success)
}

// TestGrantScope_NotFound verifies error for missing agent.
func TestGrantScope_NotFound(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	fa := &fakeAuditor{}
	mgr := NewAgentManager(registry, scopes, fa)

	_, err = mgr.GrantScope(ctx, "ghost", []string{"/global/db"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	require.Len(t, fa.events, 1)
	assert.False(t, fa.events[0].Success)
}

// TestGrantScope_EmptyInputs verifies input validation.
func TestGrantScope_EmptyInputs(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	mgr := NewAgentManager(registry, scopes, nil)

	_, err = mgr.GrantScope(ctx, "", []string{"/global/db"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent name is required")

	_, err = mgr.GrantScope(ctx, "some", []string{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one secret path is required")
}

// ------------------------------------------------------------------
// RevokeScope — unit + integration tests
// ------------------------------------------------------------------

// TestRevokeScope_HappyPath verifies that RevokeScope removes an agent's scope.
func TestRevokeScope_HappyPath(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	creator := NewAgentCreator(registry, scopes, nil)
	_, err = creator.CreateAgent(ctx, "revoke-me", "worker")
	require.NoError(t, err)

	_, err = scopes.Grant(ctx, "revoke-me", []string{"/global/db"})
	require.NoError(t, err)

	fa := &fakeAuditor{}
	mgr := NewAgentManager(registry, scopes, fa)

	err = mgr.RevokeScope(ctx, "revoke-me")
	require.NoError(t, err)

	_, err = scopes.Get(ctx, "revoke-me")
	assert.Equal(t, mongo.ErrNoDocuments, err)

	require.Len(t, fa.events, 1)
	assert.True(t, fa.events[0].Success)
}

// TestRevokeScope_NotFound verifies error for missing agent.
func TestRevokeScope_NotFound(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	fa := &fakeAuditor{}
	mgr := NewAgentManager(registry, scopes, fa)

	err = mgr.RevokeScope(ctx, "ghost")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	require.Len(t, fa.events, 1)
	assert.False(t, fa.events[0].Success)
}

// TestRevokeScope_EmptyName verifies input validation.
func TestRevokeScope_EmptyName(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	mgr := NewAgentManager(registry, scopes, nil)
	err = mgr.RevokeScope(ctx, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent name is required")
}

// ------------------------------------------------------------------
// CheckAgentActive + ErrAgentDisabled integration
// ------------------------------------------------------------------

// TestCheckAgentActive_ActiveAgent verifies no error for active agents.
func TestCheckAgentActive_ActiveAgent(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	creator := NewAgentCreator(registry, scopes, nil)
	_, err = creator.CreateAgent(ctx, "active-check", "worker")
	require.NoError(t, err)

	mgr := NewAgentManager(registry, scopes, nil)
	require.NoError(t, mgr.CheckAgentActive(ctx, "active-check"))
}

// TestCheckAgentActive_InactiveAgent returns ErrAgentDisabled.
func TestCheckAgentActive_InactiveAgent(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	creator := NewAgentCreator(registry, scopes, nil)
	_, err = creator.CreateAgent(ctx, "inactive-check", "worker")
	require.NoError(t, err)

	mgr := NewAgentManager(registry, scopes, nil)
	require.NoError(t, mgr.DisableAgent(ctx, "inactive-check"))

	err = mgr.CheckAgentActive(ctx, "inactive-check")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAgentDisabled)
}

// TestCheckAgentActive_MissingAgent returns ErrAgentDisabled.
func TestCheckAgentActive_MissingAgent(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	mgr := NewAgentManager(registry, scopes, nil)
	err = mgr.CheckAgentActive(ctx, "missing-agent")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAgentDisabled)
}

// TestCheckAgentActive_RetiredAgent returns ErrAgentDisabled.
func TestCheckAgentActive_RetiredAgent(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	creator := NewAgentCreator(registry, scopes, nil)
	_, err = creator.CreateAgent(ctx, "retired-check", "worker")
	require.NoError(t, err)

	_, err = registry.Update(ctx, "retired-check", bson.M{"status": agentregistry.AgentStatusRetired})
	require.NoError(t, err)

	mgr := NewAgentManager(registry, scopes, nil)
	err = mgr.CheckAgentActive(ctx, "retired-check")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAgentDisabled)
}

// ------------------------------------------------------------------
// Bundle request gate: disabled agent's bundle requests return ErrAgentDisabled
// ------------------------------------------------------------------

// TestBundleRequest_RejectedForDisabledAgent verifies that when an agent is
// disabled, any bundle request mediated through CheckAgentActive returns
// ErrAgentDisabled.
func TestBundleRequest_RejectedForDisabledAgent(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	registry, err := agentregistry.NewStore(client)
	require.NoError(t, err)
	scopes, err := scopestore.NewStore(client)
	require.NoError(t, err)

	creator := NewAgentCreator(registry, scopes, nil)
	_, err = creator.CreateAgent(ctx, "bundle-disabled", "worker")
	require.NoError(t, err)

	mgr := NewAgentManager(registry, scopes, nil)
	require.NoError(t, mgr.DisableAgent(ctx, "bundle-disabled"))

	// Simulate what RequestBundle would do: check agent is active first.
	err = mgr.CheckAgentActive(ctx, "bundle-disabled")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAgentDisabled)
}
