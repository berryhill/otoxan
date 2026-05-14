package identitystore

import (
	"context"
	"testing"
	"time"

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

// TestCRUD exercises the full lifecycle of an identity:
// - Create: insert a new identity
// - GetActive: retrieve the currently active identity (initially none)
// - ListVersions: list all versions of an identity
// - Activate: transactional flip to make an identity active
// - Retire: mark an identity as retired
func TestCRUD(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	store, err := NewStore(client)
	require.NoError(t, err)

	identityName := "xander"

	// ----------------------------------------------------------------
	// 1. GetActive on non-existent identity should return ErrNoActiveIdentity
	// ----------------------------------------------------------------
	_, err = store.GetActive(ctx, identityName)
	require.ErrorIs(t, err, ErrNoActiveIdentity)

	// ----------------------------------------------------------------
	// 2. ListVersions on non-existent identity should return empty slice
	// ----------------------------------------------------------------
	versions, err := store.ListVersions(ctx, identityName, false)
	require.NoError(t, err)
	assert.Empty(t, versions)

	// ----------------------------------------------------------------
	// 3. Create v1 identity (inactive by default)
	// ----------------------------------------------------------------
	now := time.Now().UTC()
	v1 := &IdentityManifest{
		Name:    identityName,
		Version: "v1",
		Status:  StatusInactive,
		Manifest: "You are Xander v1, a helpful assistant.",
		Description: "Initial version of Xander",
		Tags: []string{"initial", "test"},
		ProviderProfiles: map[ProviderType]string{
			ProviderAnthropic: "You are Xander v1.",
			ProviderOpenAI:    "You are Xander v1.",
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	res, err := store.Create(ctx, v1)
	require.NoError(t, err)
	require.NotNil(t, res.InsertedID)

	// Verify v1 was created
	v1Got, err := store.Get(ctx, identityName, "v1")
	require.NoError(t, err)
	assert.Equal(t, identityName, v1Got.Name)
	assert.Equal(t, "v1", v1Got.Version)
	assert.Equal(t, StatusInactive, v1Got.Status)
	assert.Equal(t, "You are Xander v1, a helpful assistant.", v1Got.Manifest)
	assert.Equal(t, []string{"initial", "test"}, v1Got.Tags)
	assert.Nil(t, v1Got.ActivatedAt)

	// ----------------------------------------------------------------
	// 4. Create v2 identity
	// ----------------------------------------------------------------
	v2 := &IdentityManifest{
		Name:    identityName,
		Version: "v2",
		Status:  StatusInactive,
		Manifest: "You are Xander v2, an improved assistant.",
		Description: "Second version of Xander",
		Tags: []string{"improved", "test"},
		CreatedAt: now.Add(time.Hour),
		UpdatedAt: now.Add(time.Hour),
	}

	res, err = store.Create(ctx, v2)
	require.NoError(t, err)
	require.NotNil(t, res.InsertedID)

	// ----------------------------------------------------------------
	// 5. Duplicate name+version should fail
	// ----------------------------------------------------------------
	_, err = store.Create(ctx, v1)
	require.ErrorIs(t, err, ErrIdentityExists)

	// ----------------------------------------------------------------
	// 6. ListVersions should return both v1 and v2 (sorted by created_at desc)
	// ----------------------------------------------------------------
	versions, err = store.ListVersions(ctx, identityName, false)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, "v2", versions[0].Version) // newest first
	assert.Equal(t, "v1", versions[1].Version)

	// ----------------------------------------------------------------
	// 7. Activate v1 — should work (v1 is inactive)
	// ----------------------------------------------------------------
	err = store.Activate(ctx, identityName, "v1")
	require.NoError(t, err)

	// Verify v1 is now active
	v1Got, err = store.Get(ctx, identityName, "v1")
	require.NoError(t, err)
	assert.Equal(t, StatusActive, v1Got.Status)
	require.NotNil(t, v1Got.ActivatedAt)
	assert.WithinDuration(t, now, *v1Got.ActivatedAt, time.Second)

	// ----------------------------------------------------------------
	// 8. GetActive should now return v1
	// ----------------------------------------------------------------
	active, err := store.GetActive(ctx, identityName)
	require.NoError(t, err)
	assert.Equal(t, "v1", active.Version)
	assert.Equal(t, StatusActive, active.Status)

	// ----------------------------------------------------------------
	// 9. Activate v2 — should deactivate v1 and activate v2 (transactional flip)
	// ----------------------------------------------------------------
	err = store.Activate(ctx, identityName, "v2")
	require.NoError(t, err)

	// Verify v2 is now active
	v2Got, err := store.Get(ctx, identityName, "v2")
	require.NoError(t, err)
	assert.Equal(t, StatusActive, v2Got.Status)
	require.NotNil(t, v2Got.ActivatedAt)

	// Verify v1 is now inactive (transactional flip worked)
	v1Got, err = store.Get(ctx, identityName, "v1")
	require.NoError(t, err)
	assert.Equal(t, StatusInactive, v1Got.Status)
	assert.Nil(t, v1Got.ActivatedAt)

	// GetActive should now return v2
	active, err = store.GetActive(ctx, identityName)
	require.NoError(t, err)
	assert.Equal(t, "v2", active.Version)

	// ----------------------------------------------------------------
	// 10. Cannot activate an already-active identity
	// ----------------------------------------------------------------
	err = store.Activate(ctx, identityName, "v2")
	require.ErrorIs(t, err, ErrIdentityNotInactive)

	// ----------------------------------------------------------------
	// 11. Cannot retire an active identity
	// ----------------------------------------------------------------
	err = store.Retire(ctx, identityName, "v2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot retire an active identity")

	// ----------------------------------------------------------------
	// 12. Create v3 identity and retire it
	// ----------------------------------------------------------------
	v3 := &IdentityManifest{
		Name:    identityName,
		Version: "v3",
		Status:  StatusInactive,
		Manifest: "You are Xander v3, the latest assistant.",
		Description: "Third version of Xander",
		CreatedAt: now.Add(2 * time.Hour),
		UpdatedAt: now.Add(2 * time.Hour),
	}
	res, err = store.Create(ctx, v3)
	require.NoError(t, err)

	// Retire v3
	err = store.Retire(ctx, identityName, "v3")
	require.NoError(t, err)

	// Verify v3 is retired
	v3Got, err := store.Get(ctx, identityName, "v3")
	require.NoError(t, err)
	assert.Equal(t, StatusRetired, v3Got.Status)
	require.NotNil(t, v3Got.RetiredAt)

	// ----------------------------------------------------------------
	// 13. Cannot retire an already-retired identity
	// ----------------------------------------------------------------
	err = store.Retire(ctx, identityName, "v3")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already retired")

	// ----------------------------------------------------------------
	// 14. Cannot activate a retired identity
	// ----------------------------------------------------------------
	err = store.Activate(ctx, identityName, "v3")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrIdentityNotInactive)

	// ----------------------------------------------------------------
	// 15. ListVersions should include all versions
	// ----------------------------------------------------------------
	versions, err = store.ListVersions(ctx, identityName, false)
	require.NoError(t, err)
	require.Len(t, versions, 3)
	assert.Equal(t, "v3", versions[0].Version)
	assert.Equal(t, "v2", versions[1].Version)
	assert.Equal(t, "v1", versions[2].Version)

	// ----------------------------------------------------------------
	// 16. Update an identity
	// ----------------------------------------------------------------
	_, err = store.Update(ctx, identityName, "v1", bson.M{
		"description": "Updated description for v1",
	})
	require.NoError(t, err)

	updated, err := store.Get(ctx, identityName, "v1")
	require.NoError(t, err)
	assert.Equal(t, "Updated description for v1", updated.Description)

	// ----------------------------------------------------------------
	// 17. Soft delete v1
	// ----------------------------------------------------------------
	_, err = store.Delete(ctx, identityName, "v1")
	require.NoError(t, err)

	// v1 should not be found without IncludeDeleted
	_, err = store.Get(ctx, identityName, "v1")
	require.ErrorIs(t, err, ErrIdentityNotFound)

	// v1 should be found with GetWithDeleted
	v1WithDeleted, err := store.GetWithDeleted(ctx, identityName, "v1")
	require.NoError(t, err)
	assert.Equal(t, "v1", v1WithDeleted.Version)

	// ----------------------------------------------------------------
	// 18. Restore v1
	// ----------------------------------------------------------------
	_, err = store.Restore(ctx, identityName, "v1")
	require.NoError(t, err)

	// v1 should be found again
	v1Restored, err := store.Get(ctx, identityName, "v1")
	require.NoError(t, err)
	assert.Equal(t, "v1", v1Restored.Version)

	// ----------------------------------------------------------------
	// 19. ListVersions with IncludeDeleted should include all
	// ----------------------------------------------------------------
	versions, err = store.ListVersions(ctx, identityName, true)
	require.NoError(t, err)
	require.Len(t, versions, 3)

	// ----------------------------------------------------------------
	// 20. Hard delete v3
	// ----------------------------------------------------------------
	_, err = store.HardDelete(ctx, identityName, "v3")
	require.NoError(t, err)

	// v3 should not be found even with GetWithDeleted
	_, err = store.GetWithDeleted(ctx, identityName, "v3")
	require.ErrorIs(t, err, ErrIdentityNotFound)

	// ListVersions should now only show v1 and v2
	versions, err = store.ListVersions(ctx, identityName, false)
	require.NoError(t, err)
	require.Len(t, versions, 2)
}
