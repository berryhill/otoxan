package plans

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

// newTestStore returns a PlanStore backed by a fresh test collection.
func newTestStore(t *testing.T, client *mongo.Client) *PlanStore {
	t.Helper()
	db := client.Database("silas")
	coll := db.Collection("plans")
	return NewPlanStore(coll)
}

// makeMinimalPlan returns a plan with only required fields set.
func makeMinimalPlan(id, title string) *Plan {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &Plan{
		PlanID:    id,
		Title:     title,
		Status:    StatusPlanning,
		Owner:     "silas",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// ------------------------------------------------------------------
// CRUD round-trip tests
// ------------------------------------------------------------------

func TestPlanStore_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	plan := makeMinimalPlan("plan_001", "Foundation migration")
	res, err := store.Create(ctx, plan)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if res.InsertedID == nil {
		t.Fatal("expected InsertedID")
	}

	got, err := store.Get(ctx, "plan_001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.PlanID != "plan_001" {
		t.Fatalf("expected plan_id plan_001, got %s", got.PlanID)
	}
	if got.Title != "Foundation migration" {
		t.Fatalf("expected title 'Foundation migration', got %s", got.Title)
	}
	if got.Status != StatusPlanning {
		t.Fatalf("expected status PLANNING, got %s", got.Status)
	}
	if got.Owner != "silas" {
		t.Fatalf("expected owner silas, got %s", got.Owner)
	}
	if got.PlanType != TypeStandard {
		t.Fatalf("expected default plan_type standard, got %s", got.PlanType)
	}
	if len(got.Tags) != 0 {
		t.Fatalf("expected empty tags, got %v", got.Tags)
	}
}

func TestPlanStore_CreateAppliesDefaults(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	plan := &Plan{
		PlanID:    "plan_def",
		Title:     "Default test",
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err := store.Create(ctx, plan)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "plan_def")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Status != StatusPlanning {
		t.Fatalf("expected default status PLANNING, got %s", got.Status)
	}
	if got.PlanType != TypeStandard {
		t.Fatalf("expected default plan_type standard, got %s", got.PlanType)
	}
	if got.Owner != "" {
		t.Fatalf("expected empty owner, got %s", got.Owner)
	}
}

func TestPlanStore_Update(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	plan := makeMinimalPlan("plan_upd", "Update me")
	_, err := store.Create(ctx, plan)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ures, err := store.Update(ctx, "plan_upd", bson.M{
		"status":      StatusExecuting,
		"content":     "Updated content",
		"tags":        []string{"urgent"},
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	got, err := store.Get(ctx, "plan_upd")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if got.Status != StatusExecuting {
		t.Fatalf("expected status EXECUTING, got %s", got.Status)
	}
	if got.Content != "Updated content" {
		t.Fatalf("expected content 'Updated content', got %s", got.Content)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "urgent" {
		t.Fatalf("expected tags [urgent], got %v", got.Tags)
	}
	if got.UpdatedAt.Before(plan.UpdatedAt) {
		t.Fatal("expected updated_at to be newer than created_at")
	}
}

func TestPlanStore_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	plan := makeMinimalPlan("plan_del", "Delete me")
	_, err := store.Create(ctx, plan)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.Delete(ctx, "plan_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if dres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", dres.ModifiedCount)
	}

	_, err = store.Get(ctx, "plan_del")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	got, err := store.GetWithDeleted(ctx, "plan_del")
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

func TestPlanStore_Restore(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	plan := makeMinimalPlan("plan_res", "Restore me")
	_, err := store.Create(ctx, plan)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "plan_res")

	rres, err := store.Restore(ctx, "plan_res")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on restore, got %d", rres.ModifiedCount)
	}

	got, err := store.Get(ctx, "plan_res")
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

func TestPlanStore_HardDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	plan := makeMinimalPlan("plan_hard", "Hard delete me")
	_, err := store.Create(ctx, plan)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.HardDelete(ctx, "plan_hard")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}
	if dres.DeletedCount != 1 {
		t.Fatalf("expected DeletedCount=1, got %d", dres.DeletedCount)
	}

	_, err = store.GetWithDeleted(ctx, "plan_hard")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

func TestPlanStore_List(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	plans := []*Plan{
		makeMinimalPlan("plan_l1", "Plan one"),
		makeMinimalPlan("plan_l2", "Plan two"),
		makeMinimalPlan("plan_l3", "Plan three"),
	}
	plans[0].Status = StatusPlanning
	plans[0].Tags = []string{"backend"}
	plans[1].Status = StatusExecuting
	plans[1].Tags = []string{"frontend"}
	plans[2].Status = StatusPlanning
	plans[2].Tags = []string{"backend"}

	for _, plan := range plans {
		_, err := store.Create(ctx, plan)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	all, err := store.List(ctx, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List all failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 plans, got %d", len(all))
	}

	planning, err := store.List(ctx, ListOptions{Status: []PlanStatus{StatusPlanning}})
	if err != nil {
		t.Fatalf("List planning failed: %v", err)
	}
	if len(planning) != 2 {
		t.Fatalf("expected 2 planning plans, got %d", len(planning))
	}

	backend, err := store.List(ctx, ListOptions{Tag: "backend"})
	if err != nil {
		t.Fatalf("List by tag failed: %v", err)
	}
	if len(backend) != 2 {
		t.Fatalf("expected 2 backend plans, got %d", len(backend))
	}

	limited, err := store.List(ctx, ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("List limited failed: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected 1 plan with limit, got %d", len(limited))
	}
}

func TestPlanStore_ListWithDeleted(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	plan := makeMinimalPlan("plan_ld", "List deleted")
	_, err := store.Create(ctx, plan)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "plan_ld")

	live, err := store.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("List live failed: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expected 0 live plans, got %d", len(live))
	}

	withDeleted, err := store.List(ctx, ListOptions{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("List with deleted failed: %v", err)
	}
	if len(withDeleted) != 1 {
		t.Fatalf("expected 1 plan with include_deleted, got %d", len(withDeleted))
	}
	if withDeleted[0].PlanID != "plan_ld" {
		t.Fatalf("expected plan_id plan_ld, got %s", withDeleted[0].PlanID)
	}
}

func TestPlanStore_Count(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	for i := 0; i < 5; i++ {
		plan := makeMinimalPlan(fmt.Sprintf("plan_c%d", i), fmt.Sprintf("Count plan %d", i))
		_, err := store.Create(ctx, plan)
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

func TestPlanStore_Archive(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	plan := makeMinimalPlan("plan_arch", "Archive me")
	_, err := store.Create(ctx, plan)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ares, err := store.Archive(ctx, "plan_arch")
	if err != nil {
		t.Fatalf("Archive failed: %v", err)
	}
	if ares.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ares.ModifiedCount)
	}

	got, err := store.Get(ctx, "plan_arch")
	if err != nil {
		t.Fatalf("Get after archive failed: %v", err)
	}
	if got.ArchivedAt == nil {
		t.Fatal("expected archived_at to be set")
	}

	// Should not appear in non-archived list
	live, err := store.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("List live failed: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expected 0 live plans after archive, got %d", len(live))
	}

	// Should appear with include_archived
	withArchived, err := store.List(ctx, ListOptions{IncludeArchived: true})
	if err != nil {
		t.Fatalf("List with archived failed: %v", err)
	}
	if len(withArchived) != 1 {
		t.Fatalf("expected 1 archived plan, got %d", len(withArchived))
	}

	// Unarchive
	_, err = store.Unarchive(ctx, "plan_arch")
	if err != nil {
		t.Fatalf("Unarchive failed: %v", err)
	}

	got, err = store.Get(ctx, "plan_arch")
	if err != nil {
		t.Fatalf("Get after unarchive failed: %v", err)
	}
	if got.ArchivedAt != nil {
		t.Fatal("expected archived_at to be nil after unarchive")
	}
}

// ------------------------------------------------------------------
// Fixture round-trip test
// ------------------------------------------------------------------

func TestPlanStore_FullCRUDRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	// 1. Create
	initiativeID := "init_001"
	directiveID := "dir_001"
	teamID := "team_001"
	flowSessionID := "fs_001"
	entityType := "plan"
	flowRef := "flow_001"
	planFlowID := "pf_001"

	plan := &Plan{
		PlanID:          "plan_round",
		Title:           "Round-trip plan",
		Status:          StatusPlanning,
		Owner:           "silas",
		Content:         "# Round-trip plan\n\nTest content",
		Tags:            []string{"test", "round-trip"},
		CreatedSession:  "sess_001",
		UpdatedSessions: []string{"sess_001"},
		PlanType:        TypeStandard,
		InitiativeID:    &initiativeID,
		DirectiveID:     &directiveID,
		TeamID:          &teamID,
		FlowSessionID:   &flowSessionID,
		EntityType:      &entityType,
		FlowRef:         &flowRef,
		PlanFlowID:      &planFlowID,
		CreatedAt:       time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt:       time.Now().UTC().Truncate(time.Millisecond),
	}

	_, err := store.Create(ctx, plan)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 2. Read back
	got, err := store.Get(ctx, "plan_round")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.PlanID != plan.PlanID {
		t.Fatalf("plan_id mismatch")
	}
	if got.Title != plan.Title {
		t.Fatalf("title mismatch")
	}
	if got.Status != plan.Status {
		t.Fatalf("status mismatch")
	}
	if got.Owner != plan.Owner {
		t.Fatalf("owner mismatch")
	}
	if got.Content != plan.Content {
		t.Fatalf("content mismatch")
	}
	if len(got.Tags) != 2 || got.Tags[0] != "test" || got.Tags[1] != "round-trip" {
		t.Fatalf("tags mismatch: %v", got.Tags)
	}
	if got.CreatedSession != "sess_001" {
		t.Fatalf("created_session mismatch")
	}
	if got.PlanType != plan.PlanType {
		t.Fatalf("plan_type mismatch")
	}
	if got.InitiativeID == nil || *got.InitiativeID != initiativeID {
		t.Fatalf("initiative_id mismatch")
	}
	if got.DirectiveID == nil || *got.DirectiveID != directiveID {
		t.Fatalf("directive_id mismatch")
	}
	if got.TeamID == nil || *got.TeamID != teamID {
		t.Fatalf("team_id mismatch")
	}
	if got.FlowSessionID == nil || *got.FlowSessionID != flowSessionID {
		t.Fatalf("flow_session_id mismatch")
	}
	if got.EntityType == nil || *got.EntityType != entityType {
		t.Fatalf("entity_type mismatch")
	}
	if got.FlowRef == nil || *got.FlowRef != flowRef {
		t.Fatalf("flow_ref mismatch")
	}
	if got.PlanFlowID == nil || *got.PlanFlowID != planFlowID {
		t.Fatalf("plan_flow_id mismatch")
	}

	// 3. Update
	ures, err := store.Update(ctx, "plan_round", bson.M{
		"status":  StatusExecuting,
		"content": "Updated round-trip content",
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	updated, err := store.Get(ctx, "plan_round")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if updated.Status != StatusExecuting {
		t.Fatalf("expected status EXECUTING after update, got %s", updated.Status)
	}
	if updated.Content != "Updated round-trip content" {
		t.Fatalf("expected content updated, got %s", updated.Content)
	}

	// 4. Soft delete
	_, err = store.Delete(ctx, "plan_round")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = store.Get(ctx, "plan_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	// 5. Restore
	_, err = store.Restore(ctx, "plan_round")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	restored, err := store.Get(ctx, "plan_round")
	if err != nil {
		t.Fatalf("Get after restore failed: %v", err)
	}
	if restored.Deleted {
		t.Fatal("expected deleted=false after restore")
	}
	if restored.DeletedAt != nil {
		t.Fatal("expected deleted_at=nil after restore")
	}
	if restored.Status != StatusExecuting {
		t.Fatalf("expected status preserved as EXECUTING after restore, got %s", restored.Status)
	}

	// 6. Hard delete
	_, err = store.HardDelete(ctx, "plan_round")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}

	_, err = store.GetWithDeleted(ctx, "plan_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}
