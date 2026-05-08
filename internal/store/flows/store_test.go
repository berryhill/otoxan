package flows

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

// newTestStore returns a FlowStore backed by a fresh test collection.
func newTestStore(t *testing.T, client *mongo.Client) *FlowStore {
	t.Helper()
	db := client.Database("silas")
	coll := db.Collection("flows")
	return NewFlowStore(coll)
}

// makeMinimalFlow returns a flow with only required fields set.
func makeMinimalFlow(id, name string) *Flow {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &Flow{
		FlowID:    id,
		Name:      name,
		Status:    StatusDraft,
		CreatedAt: now,
		UpdatedAt: now,
		Steps:     []FlowStep{},
	}
}

// ------------------------------------------------------------------
// CRUD round-trip tests
// ------------------------------------------------------------------

func TestFlowStore_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	flow := makeMinimalFlow("flow_001", "Onboarding flow")
	res, err := store.Create(ctx, flow)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if res.InsertedID == nil {
		t.Fatal("expected InsertedID")
	}

	got, err := store.Get(ctx, "flow_001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.FlowID != "flow_001" {
		t.Fatalf("expected flow_id flow_001, got %s", got.FlowID)
	}
	if got.Name != "Onboarding flow" {
		t.Fatalf("expected name 'Onboarding flow', got %s", got.Name)
	}
	if got.Status != StatusDraft {
		t.Fatalf("expected status DRAFT, got %s", got.Status)
	}
	if len(got.Steps) != 0 {
		t.Fatalf("expected empty steps, got %v", got.Steps)
	}
}

func TestFlowStore_CreateAppliesDefaults(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	flow := &Flow{
		FlowID:    "flow_def",
		Name:      "Default test",
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err := store.Create(ctx, flow)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "flow_def")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Status != StatusDraft {
		t.Fatalf("expected default status DRAFT, got %s", got.Status)
	}
	if len(got.Steps) != 0 {
		t.Fatalf("expected empty steps default, got %v", got.Steps)
	}
}

func TestFlowStore_Update(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	flow := makeMinimalFlow("flow_upd", "Update me")
	_, err := store.Create(ctx, flow)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ures, err := store.Update(ctx, "flow_upd", bson.M{
		"status":      StatusActive,
		"description": "Updated description",
		"tags":        []string{"urgent"},
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	got, err := store.Get(ctx, "flow_upd")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if got.Status != StatusActive {
		t.Fatalf("expected status ACTIVE, got %s", got.Status)
	}
	if got.Description != "Updated description" {
		t.Fatalf("expected description 'Updated description', got %s", got.Description)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "urgent" {
		t.Fatalf("expected tags [urgent], got %v", got.Tags)
	}
	if got.UpdatedAt.Before(flow.UpdatedAt) {
		t.Fatal("expected updated_at to be newer than created_at")
	}
}

func TestFlowStore_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	flow := makeMinimalFlow("flow_del", "Delete me")
	_, err := store.Create(ctx, flow)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.Delete(ctx, "flow_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if dres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", dres.ModifiedCount)
	}

	_, err = store.Get(ctx, "flow_del")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	got, err := store.GetWithDeleted(ctx, "flow_del")
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

func TestFlowStore_Restore(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	flow := makeMinimalFlow("flow_res", "Restore me")
	_, err := store.Create(ctx, flow)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "flow_res")

	rres, err := store.Restore(ctx, "flow_res")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on restore, got %d", rres.ModifiedCount)
	}

	got, err := store.Get(ctx, "flow_res")
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

func TestFlowStore_HardDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	flow := makeMinimalFlow("flow_hard", "Hard delete me")
	_, err := store.Create(ctx, flow)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.HardDelete(ctx, "flow_hard")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}
	if dres.DeletedCount != 1 {
		t.Fatalf("expected DeletedCount=1, got %d", dres.DeletedCount)
	}

	_, err = store.GetWithDeleted(ctx, "flow_hard")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

func TestFlowStore_List(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	flows := []*Flow{
		makeMinimalFlow("flow_l1", "Flow one"),
		makeMinimalFlow("flow_l2", "Flow two"),
		makeMinimalFlow("flow_l3", "Flow three"),
	}
	flows[0].Status = StatusDraft
	flows[1].Status = StatusActive
	flows[2].Status = StatusDraft

	for _, flow := range flows {
		_, err := store.Create(ctx, flow)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	all, err := store.List(ctx, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List all failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 flows, got %d", len(all))
	}

	draft, err := store.List(ctx, ListOptions{Status: []FlowStatus{StatusDraft}})
	if err != nil {
		t.Fatalf("List draft failed: %v", err)
	}
	if len(draft) != 2 {
		t.Fatalf("expected 2 draft flows, got %d", len(draft))
	}

	limited, err := store.List(ctx, ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("List limited failed: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected 1 flow with limit, got %d", len(limited))
	}
}

func TestFlowStore_ListWithDeleted(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	flow := makeMinimalFlow("flow_ld", "List deleted")
	_, err := store.Create(ctx, flow)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "flow_ld")

	live, err := store.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("List live failed: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expected 0 live flows, got %d", len(live))
	}

	withDeleted, err := store.List(ctx, ListOptions{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("List with deleted failed: %v", err)
	}
	if len(withDeleted) != 1 {
		t.Fatalf("expected 1 flow with include_deleted, got %d", len(withDeleted))
	}
	if withDeleted[0].FlowID != "flow_ld" {
		t.Fatalf("expected flow_id flow_ld, got %s", withDeleted[0].FlowID)
	}
}

func TestFlowStore_Count(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	for i := 0; i < 5; i++ {
		flow := makeMinimalFlow(fmt.Sprintf("flow_c%d", i), fmt.Sprintf("Count flow %d", i))
		_, err := store.Create(ctx, flow)
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

// ------------------------------------------------------------------
// Fixture round-trip test with nested steps
// ------------------------------------------------------------------

func TestFlowStore_FullCRUDRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	// 1. Create with steps
	initiativeID := "init_001"
	teamID := "team_001"
	sessionID := "sess_001"

	now := time.Now().UTC().Truncate(time.Millisecond)
	flow := &Flow{
		FlowID:       "flow_round",
		Name:         "Round-trip flow",
		Description:  "A flow with nested steps",
		Status:       StatusDraft,
		Version:      1,
		Tags:         []string{"test", "round-trip"},
		InitiativeID: &initiativeID,
		TeamID:       &teamID,
		SessionID:    &sessionID,
		CreatedAt:    now,
		UpdatedAt:    now,
		Steps: []FlowStep{
			{
				StepID:    "step_1",
				Name:      "First step",
				Type:      "action",
				Order:     1,
				Config:    map[string]interface{}{"timeout": 30},
				NextSteps: []string{"step_2"},
				CreatedAt: now,
				UpdatedAt: now,
			},
			{
				StepID:    "step_2",
				Name:      "Second step",
				Type:      "decision",
				Order:     2,
				Config:    map[string]interface{}{"condition": "success"},
				PrevSteps: []string{"step_1"},
				NextSteps: []string{},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
	}

	_, err := store.Create(ctx, flow)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 2. Read back
	got, err := store.Get(ctx, "flow_round")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.FlowID != flow.FlowID {
		t.Fatalf("flow_id mismatch")
	}
	if got.Name != flow.Name {
		t.Fatalf("name mismatch")
	}
	if got.Status != flow.Status {
		t.Fatalf("status mismatch")
	}
	if got.Version != flow.Version {
		t.Fatalf("version mismatch")
	}
	if len(got.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(got.Steps))
	}
	if got.Steps[0].StepID != "step_1" {
		t.Fatalf("expected step_1, got %s", got.Steps[0].StepID)
	}
	if got.Steps[1].StepID != "step_2" {
		t.Fatalf("expected step_2, got %s", got.Steps[1].StepID)
	}
	if got.Steps[0].Type != "action" {
		t.Fatalf("expected step type action, got %s", got.Steps[0].Type)
	}
	if len(got.Steps[0].NextSteps) != 1 || got.Steps[0].NextSteps[0] != "step_2" {
		t.Fatalf("expected next_steps [step_2], got %v", got.Steps[0].NextSteps)
	}
	if got.InitiativeID == nil || *got.InitiativeID != initiativeID {
		t.Fatalf("initiative_id mismatch")
	}
	if got.TeamID == nil || *got.TeamID != teamID {
		t.Fatalf("team_id mismatch")
	}
	if got.SessionID == nil || *got.SessionID != sessionID {
		t.Fatalf("session_id mismatch")
	}

	// 3. Update steps
	newSteps := []FlowStep{
		{
			StepID:    "step_1",
			Name:      "First step updated",
			Type:      "action",
			Order:     1,
			Config:    map[string]interface{}{"timeout": 60},
			NextSteps: []string{"step_2"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			StepID:    "step_2",
			Name:      "Second step",
			Type:      "decision",
			Order:     2,
			Config:    map[string]interface{}{"condition": "success"},
			PrevSteps: []string{"step_1"},
			NextSteps: []string{"step_3"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			StepID:    "step_3",
			Name:      "Third step",
			Type:      "end",
			Order:     3,
			Config:    map[string]interface{}{},
			PrevSteps: []string{"step_2"},
			NextSteps: []string{},
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	ures, err := store.Update(ctx, "flow_round", bson.M{"steps": newSteps, "version": 2})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	got, err = store.Get(ctx, "flow_round")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if len(got.Steps) != 3 {
		t.Fatalf("expected 3 steps after update, got %d", len(got.Steps))
	}
	if got.Steps[0].Name != "First step updated" {
		t.Fatalf("expected updated step name, got %s", got.Steps[0].Name)
	}
	if got.Version != 2 {
		t.Fatalf("expected version 2, got %d", got.Version)
	}

	// 4. Soft delete
	_, err = store.Delete(ctx, "flow_round")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = store.Get(ctx, "flow_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	// 5. Restore
	_, err = store.Restore(ctx, "flow_round")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	got, err = store.Get(ctx, "flow_round")
	if err != nil {
		t.Fatalf("Get after restore failed: %v", err)
	}
	if got.Deleted {
		t.Fatalf("expected deleted=false after restore")
	}
	if len(got.Steps) != 3 {
		t.Fatalf("expected 3 steps after restore, got %d", len(got.Steps))
	}

	// 6. Hard delete
	_, err = store.HardDelete(ctx, "flow_round")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}

	_, err = store.GetWithDeleted(ctx, "flow_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

func TestFlowStore_ZeroSteps(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	flow := makeMinimalFlow("flow_zero", "Zero steps")
	_, err := store.Create(ctx, flow)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "flow_zero")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Steps == nil {
		t.Fatal("expected non-nil steps slice, got nil")
	}
	if len(got.Steps) != 0 {
		t.Fatalf("expected 0 steps, got %d", len(got.Steps))
	}
}
