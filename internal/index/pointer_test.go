package index

import (
	"context"
	"testing"
	"time"

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

// newTestPointerStore returns a PointerStore backed by a fresh test collection.
func newTestPointerStore(t *testing.T, client *mongo.Client) *PointerStore {
	t.Helper()
	db := client.Database("test_index")
	coll := db.Collection("memory_pointers")
	return NewPointerStore(coll)
}

// ------------------------------------------------------------------
// TestPointer
// ------------------------------------------------------------------

func TestPointer_Upsert(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestPointerStore(t, client)

	p := &MemoryPointer{
		PointerID:        "ptr_001",
		SourceID:         "src_001",
		SourceType:       "session",
		SourceCollection: "sessions",
		QdrantPointID:    "qd-abc-123",
		QdrantCollection: "agent_42_index",
		SourceUpdatedAt:  time.Now().UTC().Add(-time.Hour),
	}

	res, err := store.Upsert(ctx, p)
	if err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}
	if res.UpsertedCount == 0 && res.ModifiedCount == 0 {
		t.Fatal("expected upsert to insert or modify")
	}

	// Verify the doc was inserted
	found, err := store.FindBySource(ctx, "src_001")
	if err != nil {
		t.Fatalf("FindBySource after upsert: %v", err)
	}
	if found.PointerID != "ptr_001" {
		t.Errorf("PointerID mismatch: got %q, want %q", found.PointerID, "ptr_001")
	}
	if found.QdrantPointID != "qd-abc-123" {
		t.Errorf("QdrantPointID mismatch: got %q, want %q", found.QdrantPointID, "qd-abc-123")
	}
	if found.Removed {
		t.Error("expected Removed to be false after Upsert")
	}
	if found.IndexedAt.IsZero() {
		t.Error("expected IndexedAt to be set")
	}
}

func TestPointer_UpsertUpdatesExisting(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestPointerStore(t, client)

	p := &MemoryPointer{
		PointerID:        "ptr_002",
		SourceID:         "src_002",
		SourceType:       "plan",
		SourceCollection: "plans",
		QdrantPointID:    "qd-old",
		QdrantCollection: "agent_42_index",
		SourceUpdatedAt:  time.Now().UTC().Add(-time.Hour),
	}

	if _, err := store.Upsert(ctx, p); err != nil {
		t.Fatalf("first upsert failed: %v", err)
	}

	// Update the QdrantPointID and re-upsert
	p.QdrantPointID = "qd-new"
	res, err := store.Upsert(ctx, p)
	if err != nil {
		t.Fatalf("second upsert failed: %v", err)
	}
	if res.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", res.ModifiedCount)
	}

	found, err := store.FindBySource(ctx, "src_002")
	if err != nil {
		t.Fatalf("FindBySource after update: %v", err)
	}
	if found.QdrantPointID != "qd-new" {
		t.Errorf("QdrantPointID mismatch: got %q, want %q", found.QdrantPointID, "qd-new")
	}
}

func TestPointer_FindStale(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestPointerStore(t, client)

	now := time.Now().UTC()

	// Insert two pointers
	p1 := &MemoryPointer{
		PointerID:        "ptr_stale",
		SourceID:         "src_stale",
		SourceType:       "task",
		SourceCollection: "tasks",
		QdrantPointID:    "qd-stale",
		QdrantCollection: "agent_42_index",
		SourceUpdatedAt:  now.Add(-2 * time.Hour),
	}
	p2 := &MemoryPointer{
		PointerID:        "ptr_fresh",
		SourceID:         "src_fresh",
		SourceType:       "task",
		SourceCollection: "tasks",
		QdrantPointID:    "qd-fresh",
		QdrantCollection: "agent_42_index",
		SourceUpdatedAt:  now.Add(-30 * time.Minute),
	}

	if _, err := store.Upsert(ctx, p1); err != nil {
		t.Fatalf("upsert p1: %v", err)
	}
	if _, err := store.Upsert(ctx, p2); err != nil {
		t.Fatalf("upsert p2: %v", err)
	}

	// p1 is stale because its source was updated 1 hour after SourceUpdatedAt
	staleMap := map[string]time.Time{
		"src_stale": now.Add(-1 * time.Hour),
		"src_fresh": now.Add(-1 * time.Hour),
	}

	stale, err := store.FindStale(ctx, staleMap)
	if err != nil {
		t.Fatalf("FindStale failed: %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale pointer, got %d", len(stale))
	}
	if stale[0].PointerID != "ptr_stale" {
		t.Errorf("expected stale pointer ptr_stale, got %q", stale[0].PointerID)
	}
}

func TestPointer_FindStaleEmptyMap(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestPointerStore(t, client)

	stale, err := store.FindStale(ctx, nil)
	if err != nil {
		t.Fatalf("FindStale(nil) failed: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("expected 0 stale pointers for nil map, got %d", len(stale))
	}
}

func TestPointer_MarkRemoved(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestPointerStore(t, client)

	p := &MemoryPointer{
		PointerID:        "ptr_rm",
		SourceID:         "src_rm",
		SourceType:       "build",
		SourceCollection: "builds",
		QdrantPointID:    "qd-rm",
		QdrantCollection: "agent_42_index",
		SourceUpdatedAt:  time.Now().UTC().Add(-time.Hour),
	}
	if _, err := store.Upsert(ctx, p); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	res, err := store.MarkRemoved(ctx, "ptr_rm")
	if err != nil {
		t.Fatalf("MarkRemoved failed: %v", err)
	}
	if res.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", res.ModifiedCount)
	}

	// FindBySource should now return ErrNoDocuments because the doc is removed
	_, err = store.FindBySource(ctx, "src_rm")
	if err == nil {
		t.Fatal("expected FindBySource to fail after MarkRemoved")
	}
}

func TestPointer_FindBySource(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestPointerStore(t, client)

	p := &MemoryPointer{
		PointerID:        "ptr_find",
		SourceID:         "src_find",
		SourceType:       "error",
		SourceCollection: "errors",
		QdrantPointID:    "qd-find",
		QdrantCollection: "agent_42_index",
		SourceUpdatedAt:  time.Now().UTC().Add(-time.Hour),
	}
	if _, err := store.Upsert(ctx, p); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	found, err := store.FindBySource(ctx, "src_find")
	if err != nil {
		t.Fatalf("FindBySource failed: %v", err)
	}
	if found.SourceID != "src_find" {
		t.Errorf("SourceID mismatch: got %q, want %q", found.SourceID, "src_find")
	}
	if found.SourceType != "error" {
		t.Errorf("SourceType mismatch: got %q, want %q", found.SourceType, "error")
	}

	// Non-existent source
	_, err = store.FindBySource(ctx, "src_missing")
	if err == nil {
		t.Fatal("expected FindBySource to fail for missing source")
	}
}

func TestPointer_FindBySourceExcludesRemoved(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestPointerStore(t, client)

	p := &MemoryPointer{
		PointerID:        "ptr_excl",
		SourceID:         "src_excl",
		SourceType:       "session",
		SourceCollection: "sessions",
		QdrantPointID:    "qd-excl",
		QdrantCollection: "agent_42_index",
		SourceUpdatedAt:  time.Now().UTC().Add(-time.Hour),
	}
	if _, err := store.Upsert(ctx, p); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := store.MarkRemoved(ctx, "ptr_excl"); err != nil {
		t.Fatalf("MarkRemoved: %v", err)
	}

	_, err := store.FindBySource(ctx, "src_excl")
	if err == nil {
		t.Fatal("expected FindBySource to exclude removed pointer")
	}
}

func TestPointer_Collection(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestPointerStore(t, client)

	coll := store.Collection()
	if coll == nil {
		t.Fatal("expected non-nil collection")
	}
	if coll.Name() != "memory_pointers" {
		t.Errorf("collection name mismatch: got %q, want %q", coll.Name(), "memory_pointers")
	}

	// Verify indexes were created
	indexes, err := coll.Indexes().List(ctx)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	defer indexes.Close(ctx)

	var idxDocs []bson.M
	if err := indexes.All(ctx, &idxDocs); err != nil {
		t.Fatalf("decode indexes: %v", err)
	}

	idxNames := make(map[string]bool)
	for _, d := range idxDocs {
		if name, ok := d["name"].(string); ok {
			idxNames[name] = true
		}
	}

	required := []string{"pointer_id_1", "source_id_1", "qdrant_point_id_1", "source_type_1", "removed_1", "indexed_at_1"}
	for _, name := range required {
		if !idxNames[name] {
			t.Errorf("missing index %q", name)
		}
	}
}
