package agentregistry
import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

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

// TestStore_Register_CreatesGlobalDocAndAgentDB verifies that Register("xander", "admin")
// creates the global registry document and the otoxan_agent_xander database.
func TestStore_Register_CreatesGlobalDocAndAgentDB(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	store, err := NewStore(client)
	require.NoError(t, err)
	require.NotNil(t, store)

	// Register xander as admin.
	res, err := store.Register(ctx, "xander", "admin")
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.InsertedID)

	// Verify global doc exists.
	got, err := store.Get(ctx, "xander")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "xander", got.Name)
	assert.Equal(t, "admin", got.Role)
	assert.Equal(t, "otoxan_agent_xander", got.DBName)
	assert.Equal(t, AgentStatusActive, got.Status)

	// Verify the per-agent database exists by listing its collections.
	agentDB := client.Database("otoxan_agent_xander")
	colls, err := agentDB.ListCollectionNames(ctx, bson.M{})
	require.NoError(t, err)
	assert.Contains(t, colls, "__init")
}

// TestStore_List_ReturnsRegisteredAgent verifies that List returns the registered agent.
func TestStore_List_ReturnsRegisteredAgent(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	store, err := NewStore(client)
	require.NoError(t, err)

	_, err = store.Register(ctx, "xander", "admin")
	require.NoError(t, err)

	agents, err := store.List(ctx, ListOptions{})
	require.NoError(t, err)
	require.Len(t, agents, 1)
	assert.Equal(t, "xander", agents[0].Name)
	assert.Equal(t, "admin", agents[0].Role)
	assert.Equal(t, AgentStatusActive, agents[0].Status)
}

// TestStore_Register_RejectsInvalidName verifies that Register rejects invalid agent names.
func TestStore_Register_RejectsInvalidName(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	store, err := NewStore(client)
	require.NoError(t, err)

	invalidNames := []string{"", "Xander", "x/y", "x_y", "x y"}
	for _, name := range invalidNames {
		_, err := store.Register(ctx, name, "admin")
		assert.Error(t, err, "expected error for invalid name %q", name)
	}
}

// TestStore_Register_DuplicateError verifies that registering the same agent twice fails.
func TestStore_Register_DuplicateError(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	store, err := NewStore(client)
	require.NoError(t, err)

	_, err = store.Register(ctx, "xander", "admin")
	require.NoError(t, err)

	_, err = store.Register(ctx, "xander", "admin")
	require.Error(t, err)
}

// TestStore_SoftDelete verifies soft-delete semantics.
func TestStore_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	store, err := NewStore(client)
	require.NoError(t, err)

	_, err = store.Register(ctx, "xander", "admin")
	require.NoError(t, err)

	_, err = store.Delete(ctx, "xander")
	require.NoError(t, err)

	_, err = store.Get(ctx, "xander")
	assert.Equal(t, mongo.ErrNoDocuments, err)

	got, err := store.GetWithDeleted(ctx, "xander")
	require.NoError(t, err)
	assert.True(t, got.Deleted)
	assert.NotNil(t, got.DeletedAt)
}

// TestStore_Restore verifies restore semantics.
func TestStore_Restore(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	store, err := NewStore(client)
	require.NoError(t, err)

	_, err = store.Register(ctx, "xander", "admin")
	require.NoError(t, err)

	_, _ = store.Delete(ctx, "xander")
	_, err = store.Restore(ctx, "xander")
	require.NoError(t, err)

	got, err := store.Get(ctx, "xander")
	require.NoError(t, err)
	assert.False(t, got.Deleted)
	assert.Nil(t, got.DeletedAt)
}

// TestStore_HardDelete verifies hard-delete semantics.
func TestStore_HardDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	store, err := NewStore(client)
	require.NoError(t, err)

	_, err = store.Register(ctx, "xander", "admin")
	require.NoError(t, err)

	_, err = store.HardDelete(ctx, "xander")
	require.NoError(t, err)

	_, err = store.GetWithDeleted(ctx, "xander")
	assert.Equal(t, mongo.ErrNoDocuments, err)
}

// TestStore_List_WithDeleted verifies List respects IncludeDeleted.
func TestStore_List_WithDeleted(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	store, err := NewStore(client)
	require.NoError(t, err)

	_, err = store.Register(ctx, "xander", "admin")
	require.NoError(t, err)

	_, _ = store.Delete(ctx, "xander")

	live, err := store.List(ctx, ListOptions{})
	require.NoError(t, err)
	assert.Len(t, live, 0)

	withDeleted, err := store.List(ctx, ListOptions{IncludeDeleted: true})
	require.NoError(t, err)
	assert.Len(t, withDeleted, 1)
	assert.Equal(t, "xander", withDeleted[0].Name)
}

// TestStore_List_ByStatus verifies List filtering by status.
func TestStore_List_ByStatus(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	store, err := NewStore(client)
	require.NoError(t, err)

	_, err = store.Register(ctx, "xander", "admin")
	require.NoError(t, err)

	_, err = store.Update(ctx, "xander", map[string]interface{}{"status": AgentStatusInactive})
	require.NoError(t, err)

	active, err := store.List(ctx, ListOptions{Status: []AgentStatus{AgentStatusActive}})
	require.NoError(t, err)
	assert.Len(t, active, 0)

	inactive, err := store.List(ctx, ListOptions{Status: []AgentStatus{AgentStatusInactive}})
	require.NoError(t, err)
	assert.Len(t, inactive, 1)
}
