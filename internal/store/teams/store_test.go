package teams

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

// newTestStore returns a TeamStore backed by a fresh test collection.
func newTestStore(t *testing.T, client *mongo.Client) *TeamStore {
	t.Helper()
	db := client.Database("global")
	coll := db.Collection("teams")
	return NewTeamStore(coll)
}

// makeMinimalTeam returns a team with only required fields set.
func makeMinimalTeam(id, name string) *Team {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &Team{
		TeamID: id,
		Name:   name,
		Status: StatusForming,
		DBName: fmt.Sprintf("team_%s", id),
		Members:   []Member{},
		Artifacts: map[string]interface{}{},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// ------------------------------------------------------------------
// CRUD round-trip tests
// ------------------------------------------------------------------

func TestTeamStore_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	team := makeMinimalTeam("team_001", "Backend Squad")
	res, err := store.Create(ctx, team)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if res.InsertedID == nil {
		t.Fatal("expected InsertedID")
	}

	got, err := store.Get(ctx, "team_001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.TeamID != "team_001" {
		t.Fatalf("expected team_id team_001, got %s", got.TeamID)
	}
	if got.Name != "Backend Squad" {
		t.Fatalf("expected name 'Backend Squad', got %s", got.Name)
	}
	if got.Status != StatusForming {
		t.Fatalf("expected status FORMING, got %s", got.Status)
	}
	if got.DBName != "team_team_001" {
		t.Fatalf("expected db_name team_team_001, got %s", got.DBName)
	}
	if len(got.Members) != 0 {
		t.Fatalf("expected empty members, got %v", got.Members)
	}
}

func TestTeamStore_CreateAppliesDefaults(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	team := &Team{
		TeamID:    "team_def",
		Name:      "Default test",
		CreatedAt: now,
		UpdatedAt: now,
	}

	_, err := store.Create(ctx, team)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "team_def")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Status != StatusForming {
		t.Fatalf("expected default status FORMING, got %s", got.Status)
	}
	if got.DBName != "team_team_def" {
		t.Fatalf("expected default db_name team_team_def, got %s", got.DBName)
	}
	if len(got.Members) != 0 {
		t.Fatalf("expected default empty members, got %v", got.Members)
	}
	if got.Artifacts == nil {
		t.Fatal("expected default non-nil artifacts")
	}
}

func TestTeamStore_Update(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	team := makeMinimalTeam("team_upd", "Update me")
	_, err := store.Create(ctx, team)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ures, err := store.Update(ctx, "team_upd", bson.M{
		"status": StatusActive,
		"name":   "Updated name",
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	got, err := store.Get(ctx, "team_upd")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if got.Status != StatusActive {
		t.Fatalf("expected status ACTIVE, got %s", got.Status)
	}
	if got.Name != "Updated name" {
		t.Fatalf("expected name 'Updated name', got %s", got.Name)
	}
	if got.UpdatedAt.Before(team.UpdatedAt) {
		t.Fatal("expected updated_at to be newer than created_at")
	}
}

func TestTeamStore_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	team := makeMinimalTeam("team_del", "Delete me")
	_, err := store.Create(ctx, team)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.Delete(ctx, "team_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if dres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", dres.ModifiedCount)
	}

	_, err = store.Get(ctx, "team_del")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	got, err := store.GetWithDeleted(ctx, "team_del")
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

func TestTeamStore_Restore(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	team := makeMinimalTeam("team_res", "Restore me")
	_, err := store.Create(ctx, team)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "team_res")

	rres, err := store.Restore(ctx, "team_res")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on restore, got %d", rres.ModifiedCount)
	}

	got, err := store.Get(ctx, "team_res")
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

func TestTeamStore_HardDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	team := makeMinimalTeam("team_hard", "Hard delete me")
	_, err := store.Create(ctx, team)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.HardDelete(ctx, "team_hard")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}
	if dres.DeletedCount != 1 {
		t.Fatalf("expected DeletedCount=1, got %d", dres.DeletedCount)
	}

	_, err = store.GetWithDeleted(ctx, "team_hard")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

func TestTeamStore_List(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	teams := []*Team{
		makeMinimalTeam("team_l1", "Team one"),
		makeMinimalTeam("team_l2", "Team two"),
		makeMinimalTeam("team_l3", "Team three"),
	}
	teams[0].Status = StatusForming
	teams[1].Status = StatusActive
	teams[2].Status = StatusForming

	for _, team := range teams {
		_, err := store.Create(ctx, team)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	all, err := store.List(ctx, ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List all failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 teams, got %d", len(all))
	}

	forming, err := store.List(ctx, ListOptions{Status: []TeamStatus{StatusForming}})
	if err != nil {
		t.Fatalf("List forming failed: %v", err)
	}
	if len(forming) != 2 {
		t.Fatalf("expected 2 forming teams, got %d", len(forming))
	}

	limited, err := store.List(ctx, ListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("List limited failed: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected 1 team with limit, got %d", len(limited))
	}
}

func TestTeamStore_ListWithDeleted(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	team := makeMinimalTeam("team_ld", "List deleted")
	_, err := store.Create(ctx, team)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "team_ld")

	live, err := store.List(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("List live failed: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expected 0 live teams, got %d", len(live))
	}

	withDeleted, err := store.List(ctx, ListOptions{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("List with deleted failed: %v", err)
	}
	if len(withDeleted) != 1 {
		t.Fatalf("expected 1 team with include_deleted, got %d", len(withDeleted))
	}
	if withDeleted[0].TeamID != "team_ld" {
		t.Fatalf("expected team_id team_ld, got %s", withDeleted[0].TeamID)
	}
}

func TestTeamStore_Count(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	for i := 0; i < 5; i++ {
		team := makeMinimalTeam(fmt.Sprintf("team_c%d", i), fmt.Sprintf("Count team %d", i))
		_, err := store.Create(ctx, team)
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

func TestTeamStore_AddRemoveMember(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	team := makeMinimalTeam("team_mem", "Member test")
	_, err := store.Create(ctx, team)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	member := Member{
		Agent: "silas",
		Role:  "backend_lead",
		Type:  "agent",
	}
	ares, err := store.AddMember(ctx, "team_mem", member)
	if err != nil {
		t.Fatalf("AddMember failed: %v", err)
	}
	if ares.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ares.ModifiedCount)
	}

	got, err := store.Get(ctx, "team_mem")
	if err != nil {
		t.Fatalf("Get after add failed: %v", err)
	}
	if len(got.Members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(got.Members))
	}
	if got.Members[0].Agent != "silas" {
		t.Fatalf("expected member agent silas, got %s", got.Members[0].Agent)
	}
	if got.Members[0].Role != "backend_lead" {
		t.Fatalf("expected member role backend_lead, got %s", got.Members[0].Role)
	}

	// Remove member
	rres, err := store.RemoveMember(ctx, "team_mem", "silas")
	if err != nil {
		t.Fatalf("RemoveMember failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on remove, got %d", rres.ModifiedCount)
	}

	got, err = store.Get(ctx, "team_mem")
	if err != nil {
		t.Fatalf("Get after remove failed: %v", err)
	}
	if len(got.Members) != 0 {
		t.Fatalf("expected 0 members after remove, got %d", len(got.Members))
	}
}

// ------------------------------------------------------------------
// Fixture round-trip test
// ------------------------------------------------------------------

func TestTeamStore_FullCRUDRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	// 1. Create
	directiveID := "dir_001"
	team := &Team{
		TeamID:      "team_round",
		Name:        "Round-trip team",
		DBName:      "team_round_trip",
		DirectiveID: &directiveID,
		Members: []Member{
			{Agent: "silas", Role: "lead", Type: "agent"},
			{Agent: "archer", Role: "devops", Type: "agent"},
		},
		Artifacts: map[string]interface{}{
			"repo": "github.com/silas/otoxan",
			"docs": "https://docs.otoxan.dev",
		},
		Status:    StatusActive,
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	_, err := store.Create(ctx, team)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 2. Read back
	got, err := store.Get(ctx, "team_round")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if got.TeamID != team.TeamID {
		t.Fatalf("team_id mismatch")
	}
	if got.Name != team.Name {
		t.Fatalf("name mismatch")
	}
	if got.DBName != team.DBName {
		t.Fatalf("db_name mismatch")
	}
	if got.DirectiveID == nil || *got.DirectiveID != directiveID {
		t.Fatalf("directive_id mismatch")
	}
	if len(got.Members) != 2 {
		t.Fatalf("members count mismatch: %d", len(got.Members))
	}
	if got.Members[0].Agent != "silas" || got.Members[0].Role != "lead" {
		t.Fatalf("first member mismatch: %+v", got.Members[0])
	}
	if got.Members[1].Agent != "archer" || got.Members[1].Role != "devops" {
		t.Fatalf("second member mismatch: %+v", got.Members[1])
	}
	if got.Artifacts["repo"] != "github.com/silas/otoxan" {
		t.Fatalf("artifacts repo mismatch")
	}
	if got.Status != team.Status {
		t.Fatalf("status mismatch")
	}

	// 3. Update
	ures, err := store.Update(ctx, "team_round", bson.M{
		"status": StatusPaused,
		"name":   "Updated round-trip team",
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	updated, err := store.Get(ctx, "team_round")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if updated.Status != StatusPaused {
		t.Fatalf("expected status PAUSED after update, got %s", updated.Status)
	}
	if updated.Name != "Updated round-trip team" {
		t.Fatalf("expected name updated, got %s", updated.Name)
	}

	// 4. Soft delete
	_, err = store.Delete(ctx, "team_round")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = store.Get(ctx, "team_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	// 5. Restore
	_, err = store.Restore(ctx, "team_round")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	restored, err := store.Get(ctx, "team_round")
	if err != nil {
		t.Fatalf("Get after restore failed: %v", err)
	}
	if restored.Deleted {
		t.Fatal("expected deleted=false after restore")
	}
	if restored.DeletedAt != nil {
		t.Fatal("expected deleted_at=nil after restore")
	}
	if restored.Status != StatusPaused {
		t.Fatalf("expected status preserved as PAUSED after restore, got %s", restored.Status)
	}

	// 6. Hard delete
	_, err = store.HardDelete(ctx, "team_round")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}

	_, err = store.GetWithDeleted(ctx, "team_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}
