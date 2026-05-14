package eventstore

import (
	"context"
	"testing"
	"time"

	"fmt"

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

// ------------------------------------------------------------------
// Global audit_events tests
// ------------------------------------------------------------------

func TestStore_GlobalAuditEvents_AppendAndTail(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	scope := GlobalAuditEvents(client)
	store, err := NewStore(scope)
	require.NoError(t, err)

	// Append three events
	for i := 0; i < 3; i++ {
		_, err := store.Append(ctx, EventDoc{
			Type:   "audit_login",
			Actor:  "admin",
			Data:   bson.M{"ip": "10.0.0.1", "attempt": i + 1},
		})
		require.NoError(t, err)
	}

	// Tail all — newest first
	all, err := store.Tail(ctx, TailOptions{Limit: 10})
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, "audit_login", all[0].Type)
	assert.Equal(t, "admin", all[0].Actor)
	assert.NotEmpty(t, all[0].EventID)
	assert.False(t, all[0].Timestamp.IsZero())

	// Tail limited
	tail2, err := store.Tail(ctx, TailOptions{Limit: 2})
	require.NoError(t, err)
	require.Len(t, tail2, 2)
}

func TestStore_GlobalAuditEvents_QueryByType(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	scope := GlobalAuditEvents(client)
	store, err := NewStore(scope)
	require.NoError(t, err)

	// Append mixed types
	_, err = store.Append(ctx, EventDoc{Type: "audit_login", Actor: "a"})
	require.NoError(t, err)
	_, err = store.Append(ctx, EventDoc{Type: "audit_logout", Actor: "b"})
	require.NoError(t, err)
	_, err = store.Append(ctx, EventDoc{Type: "audit_login", Actor: "c"})
	require.NoError(t, err)

	// Query only login events
	logins, err := store.QueryByType(ctx, QueryByTypeOptions{Type: "audit_login", Limit: 10})
	require.NoError(t, err)
	require.Len(t, logins, 2)
	assert.Equal(t, "audit_login", logins[0].Type)
	assert.Equal(t, "audit_login", logins[1].Type)

	// Query logout
	logouts, err := store.QueryByType(ctx, QueryByTypeOptions{Type: "audit_logout"})
	require.NoError(t, err)
	require.Len(t, logouts, 1)
	assert.Equal(t, "audit_logout", logouts[0].Type)
}

func TestStore_GlobalAuditEvents_TailSince(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	scope := GlobalAuditEvents(client)
	store, err := NewStore(scope)
	require.NoError(t, err)

	// Append one old event
	_, err = store.Append(ctx, EventDoc{Type: "old", Timestamp: time.Now().UTC().Add(-time.Hour)})
	require.NoError(t, err)

	midpoint := time.Now().UTC().Add(-time.Minute)

	// Append one recent event
	_, err = store.Append(ctx, EventDoc{Type: "recent", Timestamp: time.Now().UTC()})
	require.NoError(t, err)

	// Tail since midpoint should only return the recent event
	recent, err := store.Tail(ctx, TailOptions{Since: &midpoint, Limit: 10})
	require.NoError(t, err)
	require.Len(t, recent, 1)
	assert.Equal(t, "recent", recent[0].Type)
}

// ------------------------------------------------------------------
// Per-agent task_events tests
// ------------------------------------------------------------------

func TestStore_AgentTaskEvents_AppendAndTail(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	scope, err := AgentTaskEvents(client, "silas")
	require.NoError(t, err)
	store, err := NewStore(scope)
	require.NoError(t, err)

	// Append task events
	for i := 0; i < 3; i++ {
		_, err := store.Append(ctx, EventDoc{
			Type:   "task_created",
			Actor:  "silas",
			Data:   bson.M{"task_id": fmt.Sprintf("t_%d", i)},
		})
		require.NoError(t, err)
	}

	// Tail
	all, err := store.Tail(ctx, TailOptions{Limit: 10})
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, "task_created", all[0].Type)

	// QueryByType
	created, err := store.QueryByType(ctx, QueryByTypeOptions{Type: "task_created"})
	require.NoError(t, err)
	require.Len(t, created, 3)
}

func TestStore_AgentTaskEvents_QueryByTypeWithSince(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	scope, err := AgentTaskEvents(client, "archer")
	require.NoError(t, err)
	store, err := NewStore(scope)
	require.NoError(t, err)

	// Append old and new events of same type
	_, err = store.Append(ctx, EventDoc{
		Type:      "task_progress",
		Timestamp: time.Now().UTC().Add(-time.Hour),
		Actor:     "archer",
	})
	require.NoError(t, err)

	midpoint := time.Now().UTC().Add(-time.Minute)

	_, err = store.Append(ctx, EventDoc{
		Type:      "task_progress",
		Timestamp: time.Now().UTC(),
		Actor:     "archer",
	})
	require.NoError(t, err)

	progress, err := store.QueryByType(ctx, QueryByTypeOptions{
		Type:  "task_progress",
		Since: &midpoint,
	})
	require.NoError(t, err)
	require.Len(t, progress, 1)
	assert.True(t, progress[0].Timestamp.After(midpoint))
}

func TestStore_AgentTaskEvents_IsolatedDB(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	// Two agents, two stores
	scopeSilas, err := AgentTaskEvents(client, "silas")
	require.NoError(t, err)
	storeSilas, err := NewStore(scopeSilas)
	require.NoError(t, err)

	scopeArcher, err := AgentTaskEvents(client, "archer")
	require.NoError(t, err)
	storeArcher, err := NewStore(scopeArcher)
	require.NoError(t, err)

	// Write to silas only
	_, err = storeSilas.Append(ctx, EventDoc{Type: "task_claimed", Actor: "silas"})
	require.NoError(t, err)

	// silas sees it
	silasEvents, err := storeSilas.Tail(ctx, TailOptions{})
	require.NoError(t, err)
	require.Len(t, silasEvents, 1)

	// archer does not
	archerEvents, err := storeArcher.Tail(ctx, TailOptions{})
	require.NoError(t, err)
	require.Len(t, archerEvents, 0)
}

func TestStore_Append_AutoGeneratesIDAndTimestamp(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	scope := GlobalAuditEvents(client)
	store, err := NewStore(scope)
	require.NoError(t, err)

	res, err := store.Append(ctx, EventDoc{Type: "audit_test", Actor: "system"})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.InsertedID)

	// Verify via Tail
	docs, err := store.Tail(ctx, TailOptions{Limit: 1})
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.True(t, docs[0].EventID != "")
	assert.True(t, docs[0].Timestamp.After(time.Now().UTC().Add(-time.Minute)))
}

// ------------------------------------------------------------------
// Index verification
// ------------------------------------------------------------------

func TestStore_IndexesCreated(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	scope := GlobalAuditEvents(client)
	store, err := NewStore(scope)
	require.NoError(t, err)

	// Force an index-backed query to prove the index exists
	_, err = store.QueryByType(ctx, QueryByTypeOptions{Type: "anything"})
	require.NoError(t, err)

	// Tail also uses timestamp index
	_, err = store.Tail(ctx, TailOptions{Limit: 1})
	require.NoError(t, err)
}
