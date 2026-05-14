// Package scopestore provides a MongoDB-backed store for Infisical secret scopes.
package scopestore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
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

// TestGrantRevokeList verifies that Grant assigns scopes, List returns them,
// and Revoke removes them (via soft-delete).
func TestGrantRevokeList(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	store, err := NewStore(client)
	require.NoError(t, err)
	require.NotNil(t, store)

	// 1. Grant scopes to two agents.
	res, err := store.Grant(ctx, "xander", []string{"/global/*", "/teams/admin"})
	require.NoError(t, err)
	require.NotNil(t, res)

	res2, err := store.Grant(ctx, "silas", []string{"/global/db", "/global/api-keys"})
	require.NoError(t, err)
	require.NotNil(t, res2)

	// 2. List all scopes — should return both, newest first.
	all, err := store.List(ctx, ListOptions{})
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "silas", all[0].AgentName)   // created second = newest
	assert.Equal(t, "xander", all[1].AgentName)  // created first

	// 3. List filtered by agent name.
	xanderScopes, err := store.List(ctx, ListOptions{AgentName: "xander"})
	require.NoError(t, err)
	require.Len(t, xanderScopes, 1)
	assert.Equal(t, "xander", xanderScopes[0].AgentName)
	assert.Equal(t, []string{"/global/*", "/teams/admin"}, xanderScopes[0].SecretPaths)

	// 4. Get single agent scope.
	got, err := store.Get(ctx, "silas")
	require.NoError(t, err)
	assert.Equal(t, "silas", got.AgentName)
	assert.Equal(t, []string{"/global/db", "/global/api-keys"}, got.SecretPaths)

	// 5. Grant again for xander — should update paths.
	res3, err := store.Grant(ctx, "xander", []string{"/global/*"})
	require.NoError(t, err)
	require.NotNil(t, res3)

	updated, err := store.Get(ctx, "xander")
	require.NoError(t, err)
	assert.Equal(t, []string{"/global/*"}, updated.SecretPaths)

	// 6. Revoke xander.
	_, err = store.Revoke(ctx, "xander")
	require.NoError(t, err)

	// 7. xander should no longer appear in default List.
	remaining, err := store.List(ctx, ListOptions{})
	require.NoError(t, err)
	require.Len(t, remaining, 1)
	assert.Equal(t, "silas", remaining[0].AgentName)

	// 8. xander Get should return ErrNoDocuments.
	_, err = store.Get(ctx, "xander")
	assert.Equal(t, mongo.ErrNoDocuments, err)

	// 9. IncludeDeleted should show xander again.
	withDeleted, err := store.List(ctx, ListOptions{IncludeDeleted: true})
	require.NoError(t, err)
	require.Len(t, withDeleted, 2)

	// Find xander in the deleted list.
	var xanderDeleted *ScopeDoc
	for i := range withDeleted {
		if withDeleted[i].AgentName == "xander" {
			xanderDeleted = &withDeleted[i]
			break
		}
	}
	require.NotNil(t, xanderDeleted)
	assert.True(t, xanderDeleted.Deleted)
	assert.NotNil(t, xanderDeleted.DeletedAt)
}
