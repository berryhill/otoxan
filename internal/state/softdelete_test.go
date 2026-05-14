package state

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// testDoc is a minimal document type that embeds SoftDeleteDoc.
type testDoc struct {
	ID    bson.ObjectID `bson:"_id,omitempty"`
	Name  string        `bson:"name"`
	SoftDeleteDoc `bson:",inline"`
}

// TestSoftDelete covers default-filter, IncludeDeleted, and Restore round-trip.
func TestSoftDelete(t *testing.T) {
	uri := setupMongo(t)

	ResetClient()
	t.Cleanup(ResetClient)

	client, err := OpenClient(uri)
	require.NoError(t, err)
	require.NotNil(t, client)

	ctx := context.Background()
	coll := client.Database("otoxan_test_softdelete").Collection("docs")

	// Clean slate.
	_ = coll.Drop(ctx)

	// Insert three documents.
	doc1 := testDoc{Name: "alpha"}
	doc2 := testDoc{Name: "beta"}
	doc3 := testDoc{Name: "gamma"}

	res1, err := coll.InsertOne(ctx, doc1)
	require.NoError(t, err)
	_ = res1.InsertedID.(bson.ObjectID)

	res2, err := coll.InsertOne(ctx, doc2)
	require.NoError(t, err)
	id2 := res2.InsertedID.(bson.ObjectID)

	res3, err := coll.InsertOne(ctx, doc3)
	require.NoError(t, err)
	id3 := res3.InsertedID.(bson.ObjectID)

	// ------------------------------------------------------------------
	// 1. DefaultFilter excludes nothing initially.
	// ------------------------------------------------------------------
	cursor, err := coll.Find(ctx, DefaultFilter())
	require.NoError(t, err)
	var active []testDoc
	require.NoError(t, cursor.All(ctx, &active))
	require.Len(t, active, 3)

	// ------------------------------------------------------------------
	// 2. SoftDelete one document; DefaultFilter now excludes it.
	// ------------------------------------------------------------------
	require.NoError(t, SoftDelete(ctx, coll, id2))

	cursor, err = coll.Find(ctx, DefaultFilter())
	require.NoError(t, err)
	active = active[:0]
	require.NoError(t, cursor.All(ctx, &active))
	require.Len(t, active, 2, "DefaultFilter should hide deleted doc")
	for _, d := range active {
		assert.NotEqual(t, "beta", d.Name)
	}

	// ------------------------------------------------------------------
	// 3. IncludeDeleted shows all documents (including deleted).
	// ------------------------------------------------------------------
	cursor, err = coll.Find(ctx, IncludeDeleted())
	require.NoError(t, err)
	var all []testDoc
	require.NoError(t, cursor.All(ctx, &all))
	require.Len(t, all, 3, "IncludeDeleted should show every doc")

	var foundDeleted bool
	for _, d := range all {
		if d.Name == "beta" {
			foundDeleted = true
			assert.True(t, d.IsDeleted(), "beta should be marked deleted")
			assert.NotNil(t, d.DeletedAt)
			assert.WithinDuration(t, time.Now().UTC(), *d.DeletedAt, 5*time.Second)
		}
	}
	assert.True(t, foundDeleted, "beta should appear in IncludeDeleted results")

	// ------------------------------------------------------------------
	// 4. Restore round-trip: clear deleted_at, doc re-appears in DefaultFilter.
	// ------------------------------------------------------------------
	require.NoError(t, Restore(ctx, coll, id2))

	cursor, err = coll.Find(ctx, DefaultFilter())
	require.NoError(t, err)
	active = active[:0]
	require.NoError(t, cursor.All(ctx, &active))
	require.Len(t, active, 3, "after Restore all docs should be visible again")

	for _, d := range active {
		if d.Name == "beta" {
			assert.False(t, d.IsDeleted(), "beta should no longer be deleted")
		}
	}

	// ------------------------------------------------------------------
	// 5. SoftDelete on non-existent document returns ErrNoDocuments.
	// ------------------------------------------------------------------
	fakeID := bson.NewObjectID()
	err = SoftDelete(ctx, coll, fakeID)
	assert.ErrorIs(t, err, mongo.ErrNoDocuments)

	// ------------------------------------------------------------------
	// 6. Restore on non-existent document returns ErrNoDocuments.
	// ------------------------------------------------------------------
	err = Restore(ctx, coll, fakeID)
	assert.ErrorIs(t, err, mongo.ErrNoDocuments)

	// ------------------------------------------------------------------
	// 7. HardDelete permanently removes a document.
	// ------------------------------------------------------------------
	require.NoError(t, HardDelete(ctx, coll, id3))

	cursor, err = coll.Find(ctx, IncludeDeleted())
	require.NoError(t, err)
	all = all[:0]
	require.NoError(t, cursor.All(ctx, &all))
	require.Len(t, all, 2, "after HardDelete only 2 docs remain")
	for _, d := range all {
		assert.NotEqual(t, "gamma", d.Name)
	}
}
