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
		TeamID:    id,
		Name:      name,
		Status:    StatusForming,
		DBName:    fmt.Sprintf("team_%s", id),
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

	all, err := store.List(ctx, TeamListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List all failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 teams, got %d", len(all))
	}

	forming, err := store.List(ctx, TeamListOptions{Status: []TeamStatus{StatusForming}})
	if err != nil {
		t.Fatalf("List forming failed: %v", err)
	}
	if len(forming) != 2 {
		t.Fatalf("expected 2 forming teams, got %d", len(forming))
	}

	limited, err := store.List(ctx, TeamListOptions{Limit: 1})
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

	live, err := store.List(ctx, TeamListOptions{})
	if err != nil {
		t.Fatalf("List live failed: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expected 0 live teams, got %d", len(live))
	}

	withDeleted, err := store.List(ctx, TeamListOptions{IncludeDeleted: true})
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

// ------------------------------------------------------------------
// DirectiveStore tests
// ------------------------------------------------------------------

func newTestDirectiveStore(t *testing.T, client *mongo.Client) *DirectiveStore {
	t.Helper()
	db := client.Database("team_test")
	coll := db.Collection("directives")
	return NewDirectiveStore(coll)
}

func makeMinimalDirective(id, teamID, statement string) *Directive {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &Directive{
		DirectiveID:     id,
		TeamID:          teamID,
		Statement:       statement,
		Status:          DirectiveActive,
		Version:         1,
		SuccessCriteria: []SuccessCriterion{},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func TestDirectiveStore_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestDirectiveStore(t, client)

	d := makeMinimalDirective("dir_001", "team_a", "Grow the brand")
	res, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if res.InsertedID == nil {
		t.Fatal("expected InsertedID")
	}

	got, err := store.Get(ctx, "dir_001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.DirectiveID != "dir_001" {
		t.Fatalf("expected directive_id dir_001, got %s", got.DirectiveID)
	}
	if got.TeamID != "team_a" {
		t.Fatalf("expected team_id team_a, got %s", got.TeamID)
	}
	if got.Statement != "Grow the brand" {
		t.Fatalf("expected statement 'Grow the brand', got %s", got.Statement)
	}
	if got.Status != DirectiveActive {
		t.Fatalf("expected status ACTIVE, got %s", got.Status)
	}
	if got.Version != 1 {
		t.Fatalf("expected version 1, got %d", got.Version)
	}
}

func TestDirectiveStore_CreateAppliesDefaults(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestDirectiveStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	d := &Directive{
		DirectiveID: "dir_def",
		TeamID:      "team_b",
		Statement:   "Default test",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "dir_def")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Status != DirectiveActive {
		t.Fatalf("expected default status ACTIVE, got %s", got.Status)
	}
	if got.Version != 1 {
		t.Fatalf("expected default version 1, got %d", got.Version)
	}
	if len(got.SuccessCriteria) != 0 {
		t.Fatalf("expected default empty success_criteria, got %v", got.SuccessCriteria)
	}
}

func TestDirectiveStore_Update(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestDirectiveStore(t, client)

	d := makeMinimalDirective("dir_upd", "team_c", "Update me")
	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ures, err := store.Update(ctx, "dir_upd", bson.M{
		"statement": "Updated statement",
		"version":   2,
		"status":    DirectiveRevised,
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	got, err := store.Get(ctx, "dir_upd")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if got.Statement != "Updated statement" {
		t.Fatalf("expected statement 'Updated statement', got %s", got.Statement)
	}
	if got.Version != 2 {
		t.Fatalf("expected version 2, got %d", got.Version)
	}
	if got.Status != DirectiveRevised {
		t.Fatalf("expected status REVISED, got %s", got.Status)
	}
}

func TestDirectiveStore_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestDirectiveStore(t, client)

	d := makeMinimalDirective("dir_del", "team_d", "Delete me")
	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.Delete(ctx, "dir_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if dres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", dres.ModifiedCount)
	}

	_, err = store.Get(ctx, "dir_del")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	got, err := store.GetWithDeleted(ctx, "dir_del")
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
	store := newTestDirectiveStore(t, client)

	d := makeMinimalDirective("dir_res", "team_e", "Restore me")
	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "dir_res")

	rres, err := store.Restore(ctx, "dir_res")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on restore, got %d", rres.ModifiedCount)
	}

	got, err := store.Get(ctx, "dir_res")
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
	store := newTestDirectiveStore(t, client)

	d := makeMinimalDirective("dir_hard", "team_f", "Hard delete me")
	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.HardDelete(ctx, "dir_hard")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}
	if dres.DeletedCount != 1 {
		t.Fatalf("expected DeletedCount=1, got %d", dres.DeletedCount)
	}

	_, err = store.GetWithDeleted(ctx, "dir_hard")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

func TestDirectiveStore_List(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestDirectiveStore(t, client)

	ds := []*Directive{
		makeMinimalDirective("dir_l1", "team_g", "Directive one"),
		makeMinimalDirective("dir_l2", "team_g", "Directive two"),
		makeMinimalDirective("dir_l3", "team_h", "Directive three"),
	}
	ds[1].Status = DirectiveRetired

	for _, d := range ds {
		_, err := store.Create(ctx, d)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	all, err := store.List(ctx, DirectiveListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List all failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 directives, got %d", len(all))
	}

	byTeam, err := store.List(ctx, DirectiveListOptions{TeamID: "team_g"})
	if err != nil {
		t.Fatalf("List by team failed: %v", err)
	}
	if len(byTeam) != 2 {
		t.Fatalf("expected 2 directives for team_g, got %d", len(byTeam))
	}

	activeOnly, err := store.List(ctx, DirectiveListOptions{Status: []DirectiveStatus{DirectiveActive}})
	if err != nil {
		t.Fatalf("List active failed: %v", err)
	}
	if len(activeOnly) != 2 {
		t.Fatalf("expected 2 active directives, got %d", len(activeOnly))
	}

	limited, err := store.List(ctx, DirectiveListOptions{Limit: 1})
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
	store := newTestDirectiveStore(t, client)

	d := makeMinimalDirective("dir_ld", "team_i", "List deleted")
	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "dir_ld")

	live, err := store.List(ctx, DirectiveListOptions{})
	if err != nil {
		t.Fatalf("List live failed: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expected 0 live directives, got %d", len(live))
	}

	withDeleted, err := store.List(ctx, DirectiveListOptions{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("List with deleted failed: %v", err)
	}
	if len(withDeleted) != 1 {
		t.Fatalf("expected 1 directive with include_deleted, got %d", len(withDeleted))
	}
	if withDeleted[0].DirectiveID != "dir_ld" {
		t.Fatalf("expected directive_id dir_ld, got %s", withDeleted[0].DirectiveID)
	}
}

func TestDirectiveStore_Count(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestDirectiveStore(t, client)

	for i := 0; i < 5; i++ {
		d := makeMinimalDirective(fmt.Sprintf("dir_c%d", i), "team_j", fmt.Sprintf("Count directive %d", i))
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

func TestDirectiveStore_FullCRUDRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestDirectiveStore(t, client)

	// 1. Create
	d := &Directive{
		DirectiveID: "dir_round",
		TeamID:      "team_round",
		Statement:   "Build a sustainable open-source community",
		SuccessCriteria: []SuccessCriterion{
			{Metric: "contributor_count", Target: "50", Unit: "people"},
			{Metric: "monthly_downloads", Target: "10000", Unit: "downloads"},
		},
		Status:    DirectiveActive,
		Version:   1,
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	_, err := store.Create(ctx, d)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 2. Read back
	got, err := store.Get(ctx, "dir_round")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.DirectiveID != d.DirectiveID {
		t.Fatalf("directive_id mismatch")
	}
	if got.TeamID != d.TeamID {
		t.Fatalf("team_id mismatch")
	}
	if got.Statement != d.Statement {
		t.Fatalf("statement mismatch")
	}
	if len(got.SuccessCriteria) != 2 {
		t.Fatalf("success_criteria count mismatch: %d", len(got.SuccessCriteria))
	}
	if got.SuccessCriteria[0].Metric != "contributor_count" {
		t.Fatalf("first criterion mismatch")
	}
	if got.Status != d.Status {
		t.Fatalf("status mismatch")
	}
	if got.Version != d.Version {
		t.Fatalf("version mismatch")
	}

	// 3. Update
	ures, err := store.Update(ctx, "dir_round", bson.M{
		"statement": "Build a thriving open-source ecosystem",
		"version":   2,
		"status":    DirectiveRevised,
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	updated, err := store.Get(ctx, "dir_round")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if updated.Statement != "Build a thriving open-source ecosystem" {
		t.Fatalf("expected statement updated, got %s", updated.Statement)
	}
	if updated.Version != 2 {
		t.Fatalf("expected version 2 after update, got %d", updated.Version)
	}
	if updated.Status != DirectiveRevised {
		t.Fatalf("expected status REVISED after update, got %s", updated.Status)
	}

	// 4. Soft delete
	_, err = store.Delete(ctx, "dir_round")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = store.Get(ctx, "dir_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	// 5. Restore
	_, err = store.Restore(ctx, "dir_round")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	restored, err := store.Get(ctx, "dir_round")
	if err != nil {
		t.Fatalf("Get after restore failed: %v", err)
	}
	if restored.Deleted {
		t.Fatal("expected deleted=false after restore")
	}
	if restored.DeletedAt != nil {
		t.Fatal("expected deleted_at=nil after restore")
	}
	if restored.Version != 2 {
		t.Fatalf("expected version preserved as 2 after restore, got %d", restored.Version)
	}

	// 6. Hard delete
	_, err = store.HardDelete(ctx, "dir_round")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}

	_, err = store.GetWithDeleted(ctx, "dir_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

// ------------------------------------------------------------------
// InitiativeStore tests
// ------------------------------------------------------------------

func newTestInitiativeStore(t *testing.T, client *mongo.Client) *InitiativeStore {
	t.Helper()
	db := client.Database("team_test")
	coll := db.Collection("initiatives")
	return NewInitiativeStore(coll)
}

func makeMinimalInitiative(id, directiveID, teamID, title string) *Initiative {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &Initiative{
		InitiativeID:    id,
		DirectiveID:     directiveID,
		TeamID:          teamID,
		Title:           title,
		Description:     "Test initiative description",
		Status:          InitiativeProposed,
		PlanIDs:         []string{},
		SuccessCriteria: []SuccessCriterion{},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func TestInitiativeStore_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestInitiativeStore(t, client)

	in := makeMinimalInitiative("init_001", "dir_001", "team_a", "Grow Instagram")
	res, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if res.InsertedID == nil {
		t.Fatal("expected InsertedID")
	}

	got, err := store.Get(ctx, "init_001")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.InitiativeID != "init_001" {
		t.Fatalf("expected initiative_id init_001, got %s", got.InitiativeID)
	}
	if got.DirectiveID != "dir_001" {
		t.Fatalf("expected directive_id dir_001, got %s", got.DirectiveID)
	}
	if got.TeamID != "team_a" {
		t.Fatalf("expected team_id team_a, got %s", got.TeamID)
	}
	if got.Title != "Grow Instagram" {
		t.Fatalf("expected title 'Grow Instagram', got %s", got.Title)
	}
	if got.Status != InitiativeProposed {
		t.Fatalf("expected status PROPOSED, got %s", got.Status)
	}
	if len(got.PlanIDs) != 0 {
		t.Fatalf("expected empty plan_ids, got %v", got.PlanIDs)
	}
}

func TestInitiativeStore_CreateAppliesDefaults(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestInitiativeStore(t, client)

	now := time.Now().UTC().Truncate(time.Millisecond)
	in := &Initiative{
		InitiativeID: "init_def",
		DirectiveID:  "dir_002",
		TeamID:       "team_b",
		Title:        "Default test",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	_, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := store.Get(ctx, "init_def")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.Status != InitiativeProposed {
		t.Fatalf("expected default status PROPOSED, got %s", got.Status)
	}
	if len(got.PlanIDs) != 0 {
		t.Fatalf("expected default empty plan_ids, got %v", got.PlanIDs)
	}
	if len(got.SuccessCriteria) != 0 {
		t.Fatalf("expected default empty success_criteria, got %v", got.SuccessCriteria)
	}
}

func TestInitiativeStore_Update(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestInitiativeStore(t, client)

	in := makeMinimalInitiative("init_upd", "dir_003", "team_c", "Update me")
	_, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	ures, err := store.Update(ctx, "init_upd", bson.M{
		"status":      InitiativeActive,
		"title":       "Updated title",
		"description": "Updated description",
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	got, err := store.Get(ctx, "init_upd")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if got.Status != InitiativeActive {
		t.Fatalf("expected status ACTIVE, got %s", got.Status)
	}
	if got.Title != "Updated title" {
		t.Fatalf("expected title 'Updated title', got %s", got.Title)
	}
	if got.Description != "Updated description" {
		t.Fatalf("expected description 'Updated description', got %s", got.Description)
	}
}

func TestInitiativeStore_SoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestInitiativeStore(t, client)

	in := makeMinimalInitiative("init_del", "dir_004", "team_d", "Delete me")
	_, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.Delete(ctx, "init_del")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if dres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", dres.ModifiedCount)
	}

	_, err = store.Get(ctx, "init_del")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	got, err := store.GetWithDeleted(ctx, "init_del")
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

func TestInitiativeStore_Restore(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestInitiativeStore(t, client)

	in := makeMinimalInitiative("init_res", "dir_005", "team_e", "Restore me")
	_, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "init_res")

	rres, err := store.Restore(ctx, "init_res")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on restore, got %d", rres.ModifiedCount)
	}

	got, err := store.Get(ctx, "init_res")
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

func TestInitiativeStore_HardDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestInitiativeStore(t, client)

	in := makeMinimalInitiative("init_hard", "dir_006", "team_f", "Hard delete me")
	_, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	dres, err := store.HardDelete(ctx, "init_hard")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}
	if dres.DeletedCount != 1 {
		t.Fatalf("expected DeletedCount=1, got %d", dres.DeletedCount)
	}

	_, err = store.GetWithDeleted(ctx, "init_hard")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

func TestInitiativeStore_List(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestInitiativeStore(t, client)

	ins := []*Initiative{
		makeMinimalInitiative("init_l1", "dir_007", "team_g", "Initiative one"),
		makeMinimalInitiative("init_l2", "dir_007", "team_g", "Initiative two"),
		makeMinimalInitiative("init_l3", "dir_008", "team_h", "Initiative three"),
	}
	ins[1].Status = InitiativeActive

	for _, in := range ins {
		_, err := store.Create(ctx, in)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	}

	all, err := store.List(ctx, InitiativeListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("List all failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 initiatives, got %d", len(all))
	}

	byTeam, err := store.List(ctx, InitiativeListOptions{TeamID: "team_g"})
	if err != nil {
		t.Fatalf("List by team failed: %v", err)
	}
	if len(byTeam) != 2 {
		t.Fatalf("expected 2 initiatives for team_g, got %d", len(byTeam))
	}

	byDirective, err := store.List(ctx, InitiativeListOptions{DirectiveID: "dir_007"})
	if err != nil {
		t.Fatalf("List by directive failed: %v", err)
	}
	if len(byDirective) != 2 {
		t.Fatalf("expected 2 initiatives for dir_007, got %d", len(byDirective))
	}

	activeOnly, err := store.List(ctx, InitiativeListOptions{Status: []InitiativeStatus{InitiativeActive}})
	if err != nil {
		t.Fatalf("List active failed: %v", err)
	}
	if len(activeOnly) != 1 {
		t.Fatalf("expected 1 active initiative, got %d", len(activeOnly))
	}

	limited, err := store.List(ctx, InitiativeListOptions{Limit: 1})
	if err != nil {
		t.Fatalf("List limited failed: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("expected 1 initiative with limit, got %d", len(limited))
	}
}

func TestInitiativeStore_ListWithDeleted(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestInitiativeStore(t, client)

	in := makeMinimalInitiative("init_ld", "dir_009", "team_i", "List deleted")
	_, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	_, _ = store.Delete(ctx, "init_ld")

	live, err := store.List(ctx, InitiativeListOptions{})
	if err != nil {
		t.Fatalf("List live failed: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("expected 0 live initiatives, got %d", len(live))
	}

	withDeleted, err := store.List(ctx, InitiativeListOptions{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("List with deleted failed: %v", err)
	}
	if len(withDeleted) != 1 {
		t.Fatalf("expected 1 initiative with include_deleted, got %d", len(withDeleted))
	}
	if withDeleted[0].InitiativeID != "init_ld" {
		t.Fatalf("expected initiative_id init_ld, got %s", withDeleted[0].InitiativeID)
	}
}

func TestInitiativeStore_Count(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestInitiativeStore(t, client)

	for i := 0; i < 5; i++ {
		in := makeMinimalInitiative(fmt.Sprintf("init_c%d", i), fmt.Sprintf("dir_%d", i), "team_j", fmt.Sprintf("Count initiative %d", i))
		_, err := store.Create(ctx, in)
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

func TestInitiativeStore_AddPlanToInitiative(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestInitiativeStore(t, client)

	in := makeMinimalInitiative("init_plan", "dir_010", "team_k", "Plan linkage test")
	_, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Add plan to initiative (no plan collection for bidirectional update)
	res, err := store.AddPlanToInitiative(ctx, "init_plan", "plan_001", nil)
	if err != nil {
		t.Fatalf("AddPlanToInitiative failed: %v", err)
	}
	if res.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", res.ModifiedCount)
	}

	got, err := store.Get(ctx, "init_plan")
	if err != nil {
		t.Fatalf("Get after add failed: %v", err)
	}
	if len(got.PlanIDs) != 1 {
		t.Fatalf("expected 1 plan_id, got %d", len(got.PlanIDs))
	}
	if got.PlanIDs[0] != "plan_001" {
		t.Fatalf("expected plan_id plan_001, got %s", got.PlanIDs[0])
	}

	// Add same plan again — $addToSet is idempotent but $set on updated_at
	// always bumps ModifiedCount, so we just verify the array stays at 1.
	res2, err := store.AddPlanToInitiative(ctx, "init_plan", "plan_001", nil)
	if err != nil {
		t.Fatalf("AddPlanToInitiative idempotent failed: %v", err)
	}
	_ = res2 // ModifiedCount may be 1 because $set updates updated_at

	got2, err := store.Get(ctx, "init_plan")
	if err != nil {
		t.Fatalf("Get after idempotent add failed: %v", err)
	}
	if len(got2.PlanIDs) != 1 {
		t.Fatalf("expected still 1 plan_id after idempotent add, got %d", len(got2.PlanIDs))
	}

	// Add a second plan
	res3, err := store.AddPlanToInitiative(ctx, "init_plan", "plan_002", nil)
	if err != nil {
		t.Fatalf("AddPlanToInitiative second plan failed: %v", err)
	}
	if res3.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on second plan, got %d", res3.ModifiedCount)
	}

	got3, err := store.Get(ctx, "init_plan")
	if err != nil {
		t.Fatalf("Get after second add failed: %v", err)
	}
	if len(got3.PlanIDs) != 2 {
		t.Fatalf("expected 2 plan_ids, got %d", len(got3.PlanIDs))
	}
}

func TestInitiativeStore_AddPlanToInitiative_Bidirectional(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	initStore := newTestInitiativeStore(t, client)
	planColl := client.Database("team_test").Collection("plans")

	in := makeMinimalInitiative("init_bidir", "dir_011", "team_l", "Bidirectional test")
	_, err := initStore.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Seed a plan document
	_, err = planColl.InsertOne(ctx, bson.M{
		"plan_id":       "plan_bidir",
		"title":         "Bidirectional plan",
		"status":        "PLANNING",
		"created_at":    time.Now().UTC(),
		"updated_at":    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Plan insert failed: %v", err)
	}

	// Add plan to initiative with bidirectional update
	res, err := initStore.AddPlanToInitiative(ctx, "init_bidir", "plan_bidir", planColl)
	if err != nil {
		t.Fatalf("AddPlanToInitiative failed: %v", err)
	}
	if res.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", res.ModifiedCount)
	}

	// Verify initiative side
	gotIn, err := initStore.Get(ctx, "init_bidir")
	if err != nil {
		t.Fatalf("Get initiative failed: %v", err)
	}
	if len(gotIn.PlanIDs) != 1 || gotIn.PlanIDs[0] != "plan_bidir" {
		t.Fatalf("expected plan_id plan_bidir in initiative, got %v", gotIn.PlanIDs)
	}

	// Verify plan side (bidirectional)
	var planDoc bson.M
	err = planColl.FindOne(ctx, bson.M{"plan_id": "plan_bidir"}).Decode(&planDoc)
	if err != nil {
		t.Fatalf("Find plan failed: %v", err)
	}
	if planDoc["initiative_id"] != "init_bidir" {
		t.Fatalf("expected initiative_id init_bidir on plan, got %v", planDoc["initiative_id"])
	}
}

func TestInitiativeStore_RemovePlanFromInitiative(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestInitiativeStore(t, client)

	in := makeMinimalInitiative("init_rm", "dir_012", "team_m", "Remove plan test")
	in.PlanIDs = []string{"plan_001", "plan_002"}
	_, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	res, err := store.RemovePlanFromInitiative(ctx, "init_rm", "plan_001")
	if err != nil {
		t.Fatalf("RemovePlanFromInitiative failed: %v", err)
	}
	if res.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", res.ModifiedCount)
	}

	got, err := store.Get(ctx, "init_rm")
	if err != nil {
		t.Fatalf("Get after remove failed: %v", err)
	}
	if len(got.PlanIDs) != 1 {
		t.Fatalf("expected 1 plan_id after remove, got %d", len(got.PlanIDs))
	}
	if got.PlanIDs[0] != "plan_002" {
		t.Fatalf("expected remaining plan_id plan_002, got %s", got.PlanIDs[0])
	}
}

func TestInitiativeStore_FullCRUDRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestInitiativeStore(t, client)

	// 1. Create
	in := &Initiative{
		InitiativeID: "init_round",
		DirectiveID:  "dir_round",
		TeamID:       "team_round",
		Title:        "Round-trip initiative",
		Description:  "A complete initiative for testing",
		SuccessCriteria: []SuccessCriterion{
			{Metric: "follower_count", Target: "5000", Unit: "followers"},
		},
		Timeline: Timeline{
			StartedAt:        strPtr("2024-01-01"),
			TargetCompletion: strPtr("2024-06-01"),
		},
		PlanIDs: []string{},
		Status:  InitiativeActive,
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	_, err := store.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// 2. Read back
	got, err := store.Get(ctx, "init_round")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.InitiativeID != in.InitiativeID {
		t.Fatalf("initiative_id mismatch")
	}
	if got.DirectiveID != in.DirectiveID {
		t.Fatalf("directive_id mismatch")
	}
	if got.TeamID != in.TeamID {
		t.Fatalf("team_id mismatch")
	}
	if got.Title != in.Title {
		t.Fatalf("title mismatch")
	}
	if got.Description != in.Description {
		t.Fatalf("description mismatch")
	}
	if len(got.SuccessCriteria) != 1 {
		t.Fatalf("success_criteria count mismatch: %d", len(got.SuccessCriteria))
	}
	if got.SuccessCriteria[0].Metric != "follower_count" {
		t.Fatalf("first criterion mismatch")
	}
	if got.Status != in.Status {
		t.Fatalf("status mismatch")
	}
	if got.Timeline.StartedAt == nil || *got.Timeline.StartedAt != "2024-01-01" {
		t.Fatalf("timeline started_at mismatch")
	}

	// 3. Update
	ures, err := store.Update(ctx, "init_round", bson.M{
		"status":      InitiativeMeasuring,
		"title":       "Updated round-trip initiative",
		"outcome_notes": "Making good progress",
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	updated, err := store.Get(ctx, "init_round")
	if err != nil {
		t.Fatalf("Get after update failed: %v", err)
	}
	if updated.Status != InitiativeMeasuring {
		t.Fatalf("expected status MEASURING after update, got %s", updated.Status)
	}
	if updated.Title != "Updated round-trip initiative" {
		t.Fatalf("expected title updated, got %s", updated.Title)
	}
	if updated.OutcomeNotes != "Making good progress" {
		t.Fatalf("expected outcome_notes updated, got %s", updated.OutcomeNotes)
	}

	// 4. Soft delete
	_, err = store.Delete(ctx, "init_round")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = store.Get(ctx, "init_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	// 5. Restore
	_, err = store.Restore(ctx, "init_round")
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	restored, err := store.Get(ctx, "init_round")
	if err != nil {
		t.Fatalf("Get after restore failed: %v", err)
	}
	if restored.Deleted {
		t.Fatal("expected deleted=false after restore")
	}
	if restored.DeletedAt != nil {
		t.Fatal("expected deleted_at=nil after restore")
	}
	if restored.Status != InitiativeMeasuring {
		t.Fatalf("expected status preserved as MEASURING after restore, got %s", restored.Status)
	}

	// 6. Hard delete
	_, err = store.HardDelete(ctx, "init_round")
	if err != nil {
		t.Fatalf("HardDelete failed: %v", err)
	}

	_, err = store.GetWithDeleted(ctx, "init_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

func strPtr(s string) *string {
	return &s
}
