package directives

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// setupMongo spins up a testcontainers MongoDB container and returns a client.
// It also sets MONGO_URI in the environment so the Python bridge can connect
// to the same instance.
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
	// Export so Python helper sees the same URI
	os.Setenv("MONGO_URI", uri)

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("failed to connect to mongodb: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Disconnect(ctx)
	})

	return client
}

// newTestStore returns a DirectiveStore backed by a fresh test collection.
func newTestStore(t *testing.T, client *mongo.Client) *DirectiveStore {
	t.Helper()
	db := client.Database("silas")
	coll := db.Collection("directives")
	return NewDirectiveStore(coll)
}

// makeMinimalDirective returns a directive with only required fields set.
func makeMinimalDirective(id, title string) *Directive {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &Directive{
		DirectiveID: id,
		Title:       title,
		Content:     "Test directive content",
		Category:    "general",
		Priority:    0,
		Enabled:     true,
		Tags:        []string{},
		Owner:       "silas",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

// ------------------------------------------------------------------
// CRUD round-trip tests
// ------------------------------------------------------------------

func TestDirectiveStore_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	d := makeMinimalDirective("d_001", "Always confirm")
	res, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if res.InsertedID == nil {
		t.Fatal("expected InsertedID")
	}

	got, err := store.Get(ctx, "d_001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.DirectiveID != "d_001" {
		t.Fatalf("expected directive_id d_001, got %s", got.DirectiveID)
	}
	if got.Title != "Always confirm" {
		t.Fatalf("expected title 'Always confirm', got %s", got.Title)
	}
	if got.Category != "general" {
		t.Fatalf("expected category general, got %s", got.Category)
	}
	if got.Priority != 0 {
		t.Fatalf("expected priority 0, got %d", got.Priority)
	}
	if !got.Enabled {
		t.Fatal("expected enabled=true")
	}
	if got.Owner != "silas" {
		t.Fatalf("expected owner silas, got %s", got.Owner)
	}
}

func TestDirectiveStore_CreateAppliesDefaults(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	d := &Directive{
		DirectiveID: "d_def",
		Title:       "Default test",
		Content:     "Content",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "d_def")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Category != "general" {
		t.Fatalf("expected default category general, got %s", got.Category)
	}
	if got.Priority != 0 {
		t.Fatalf("expected default priority 0, got %d", got.Priority)
	}
	if !got.Enabled {
		t.Fatal("expected default enabled=true")
	}
	if len(got.Tags) != 0 {
		t.Fatalf("expected default empty tags, got %v", got.Tags)
	}
}

func TestDirectiveStore_Update(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	d := makeMinimalDirective("d_upd", "Update me")
	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ures, err := store.Update(ctx, "d_upd", bson.M{
		"priority": 50,
		"content":  "Updated content",
		"enabled":  false,
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	got, err := store.Get(ctx, "d_upd")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if got.Priority != 50 {
		t.Fatalf("expected priority 50, got %d", got.Priority)
	}
	if got.Content != "Updated content" {
		t.Fatalf("expected content 'Updated content', got %s", got.Content)
	}
	if got.Enabled {
		t.Fatal("expected enabled=false")
	}
	if got.UpdatedAt.Before(d.UpdatedAt) {
		t.Fatal("expected updated_at to be newer than created_at")
	}
}

func TestDirectiveStore_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	d := makeMinimalDirective("d_del", "Delete me")
	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.Delete(ctx, "d_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if dres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", dres.ModifiedCount)
	}

	_, err = store.Get(ctx, "d_del")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	got, err := store.GetWithDeleted(ctx, "d_del")
	if err != nil {
		t.Fatalf("GetWithDeleted failed: %v", err)
	}
	if !got.Deleted {
		t.Fatalf("expected deleted=true, got %v", got.Deleted)
	}
	if got.DeletedAt == nil {
		t.Fatal("expected deleted_at to be set")
	}
}

func TestDirectiveStore_Restore(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	d := makeMinimalDirective("d_res", "Restore me")
	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "d_res")

	rres, err := store.Restore(ctx, "d_res")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on restore, got %d", rres.ModifiedCount)
	}

	got, err := store.Get(ctx, "d_res")
	if err != nil {
		t.Fatalf("Get after restore failed: %v", err)
	}
	if got.Deleted {
		t.Fatalf("expected deleted=false after restore, got %v", got.Deleted)
	}
	if got.DeletedAt != nil {
		t.Fatalf("expected deleted_at nil after restore, got %v", got.DeletedAt)
	}
}

func TestDirectiveStore_HardDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	d := makeMinimalDirective("d_hard", "Hard delete me")
	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.HardDelete(ctx, "d_hard")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}
	if dres.DeletedCount != 1 {
		t.Fatalf("expected DeletedCount=1, got %d", dres.DeletedCount)
	}

	_, err = store.GetWithDeleted(ctx, "d_hard")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

func TestDirectiveStore_List(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	ds := []*Directive{
		makeMinimalDirective("d_l1", "Directive one"),
		makeMinimalDirective("d_l2", "Directive two"),
		makeMinimalDirective("d_l3", "Directive three"),
	}
	ds[0].Category = "behavior"
	ds[0].Priority = 10
	ds[1].Category = "safety"
	ds[1].Priority = 80
	ds[2].Category = "behavior"
	ds[2].Priority = 20

	for _, d := range ds {
		_, err := store.Create(ctx, d)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	// Explicitly disable ds[1] after creation (Create defaults enabled=true)
	_, err := store.Update(ctx, "d_l2", bson.M{"enabled": false})
	if err != nil {
		t.Fatalf("Update to disable failed: %v", err)
	}

	all, err := store.List(ctx, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List all failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 directives, got %d", len(all))
	}

	// Highest priority first
	if all[0].Priority != 80 {
		t.Fatalf("expected first directive priority=80, got %d", all[0].Priority)
	}

	behavior, err := store.List(ctx, ListOptions{Category: "behavior"})
	if err != nil {
		t.Fatalf("List by category failed: %v", err)
	}
	if len(behavior) != 2 {
		t.Fatalf("expected 2 behavior directives, got %d", len(behavior))
	}

	enabledOnly, err := store.List(ctx, ListOptions{EnabledOnly: true})
	if err != nil {
		t.Fatalf("List enabled only failed: %v", err)
	}
	if len(enabledOnly) != 2 {
		t.Fatalf("expected 2 enabled directives, got %d", len(enabledOnly))
	}

	limited, err := store.List(ctx, ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("List limited failed: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected 1 directive with limit, got %d", len(limited))
	}
}

func TestDirectiveStore_ListWithDeleted(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	d := makeMinimalDirective("d_ld", "List deleted")
	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "d_ld")

	live, err := store.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("List live failed: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expected 0 live directives, got %d", len(live))
	}

	withDeleted, err := store.List(ctx, ListOptions{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("List with deleted failed: %v", err)
	}
	if len(withDeleted) != 1 {
		t.Fatalf("expected 1 directive with include_deleted, got %d", len(withDeleted))
	}
	if withDeleted[0].DirectiveID != "d_ld" {
		t.Fatalf("expected directive_id d_ld, got %s", withDeleted[0].DirectiveID)
	}
}

func TestDirectiveStore_Count(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	for i := 0; i < 5; i++ {
		d := makeMinimalDirective(fmt.Sprintf("d_c%d", i), fmt.Sprintf("Count directive %d", i))
		_, err := store.Create(ctx, d)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	cnt, err := store.Count(ctx, bson.M{})
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if cnt != 5 {
		t.Fatalf("expected count 5, got %d", cnt)
	}
}

func TestDirectiveStore_Upsert(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	// First upsert — insert
	d := &Directive{
		DirectiveID: "d_upst",
		Title:       "Upsert test",
		Content:     "Initial content",
		Category:    "behavior",
		Priority:    30,
		Enabled:     true,
		Tags:        []string{"test"},
		Owner:       "silas",
		CreatedAt:   time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt:   time.Now().UTC().Truncate(time.Millisecond),
	}

	ures, err := store.Upsert(ctx, d)
	if err != nil {
		t.Fatalf("Upsert insert failed: %v", err)
	}
	if ures.UpsertedCount != 1 && ures.ModifiedCount != 1 {
		t.Fatalf("expected upsert insert (UpsertedCount=1 or ModifiedCount=1), got UpsertedCount=%d ModifiedCount=%d", ures.UpsertedCount, ures.ModifiedCount)
	}

	got, err := store.Get(ctx, "d_upst")
	if err != nil {
		t.Fatalf("Get after upsert insert failed: %v", err)
	}
	if got.Title != "Upsert test" {
		t.Fatalf("title mismatch after upsert insert")
	}
	if got.Priority != 30 {
		t.Fatalf("priority mismatch after upsert insert")
	}

	// Second upsert — update
	d2 := &Directive{
		DirectiveID: "d_upst",
		Title:       "Upsert test updated",
		Content:     "Updated content",
		Category:    "safety",
		Priority:    60,
		Enabled:     false,
		Tags:        []string{"test", "updated"},
		Owner:       "archer",
		UpdatedAt:   time.Now().UTC().Truncate(time.Millisecond),
	}

	ures2, err := store.Upsert(ctx, d2)
	if err != nil {
		t.Fatalf("Upsert update failed: %v", err)
	}
	if ures2.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on upsert update, got %d", ures2.ModifiedCount)
	}

	got2, err := store.Get(ctx, "d_upst")
	if err != nil {
		t.Fatalf("Get after upsert update failed: %v", err)
	}
	if got2.Title != "Upsert test updated" {
		t.Fatalf("title mismatch after upsert update, got %s", got2.Title)
	}
	if got2.Priority != 60 {
		t.Fatalf("priority mismatch after upsert update, got %d", got2.Priority)
	}
	if got2.Enabled {
		t.Fatal("expected enabled=false after upsert update")
	}
	if got2.Category != "safety" {
		t.Fatalf("category mismatch after upsert update, got %s", got2.Category)
	}
	// Owner should remain from first insert (setOnInsert)
	if got2.Owner != "silas" {
		t.Fatalf("owner should remain from first insert, got %s", got2.Owner)
	}
}

// ------------------------------------------------------------------
// Fixture round-trip test
// ------------------------------------------------------------------

func TestDirectiveStore_FullCRUDRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	// 1. Create
	d := &Directive{
		DirectiveID: "d_round",
		Title:       "Round-trip directive",
		Content:     "Ask before executing any destructive operation",
		Category:    "safety",
		Priority:    90,
		Enabled:     true,
		Tags:        []string{"critical", "behavior"},
		Owner:       "silas",
		CreatedAt:   time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt:   time.Now().UTC().Truncate(time.Millisecond),
	}

	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 2. Read back
	got, err := store.Get(ctx, "d_round")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.DirectiveID != d.DirectiveID {
		t.Fatalf("directive_id mismatch")
	}
	if got.Title != d.Title {
		t.Fatalf("title mismatch")
	}
	if got.Content != d.Content {
		t.Fatalf("content mismatch")
	}
	if got.Category != d.Category {
		t.Fatalf("category mismatch")
	}
	if got.Priority != d.Priority {
		t.Fatalf("priority mismatch")
	}
	if got.Enabled != d.Enabled {
		t.Fatalf("enabled mismatch")
	}
	if len(got.Tags) != 2 || got.Tags[0] != "critical" || got.Tags[1] != "behavior" {
		t.Fatalf("tags mismatch: %v", got.Tags)
	}
	if got.Owner != d.Owner {
		t.Fatalf("owner mismatch")
	}

	// 3. Update
	ures, err := store.Update(ctx, "d_round", bson.M{
		"priority": 95,
		"content":  "Always confirm before executing destructive operations",
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	updated, err := store.Get(ctx, "d_round")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if updated.Priority != 95 {
		t.Fatalf("expected priority 95 after update, got %d", updated.Priority)
	}
	if updated.Content != "Always confirm before executing destructive operations" {
		t.Fatalf("expected content updated, got %s", updated.Content)
	}

	// 4. Soft delete
	_, err = store.Delete(ctx, "d_round")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = store.Get(ctx, "d_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	// 5. Restore
	_, err = store.Restore(ctx, "d_round")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	restored, err := store.Get(ctx, "d_round")
	if err != nil {
		t.Fatalf("Get after restore failed: %v", err)
	}
	if restored.Deleted {
		t.Fatal("expected deleted=false after restore")
	}
	if restored.DeletedAt != nil {
		t.Fatal("expected deleted_at=nil after restore")
	}
	if restored.Priority != 95 {
		t.Fatalf("expected priority preserved as 95 after restore, got %d", restored.Priority)
	}

	// 6. Hard delete
	_, err = store.HardDelete(ctx, "d_round")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}

	_, err = store.GetWithDeleted(ctx, "d_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}
