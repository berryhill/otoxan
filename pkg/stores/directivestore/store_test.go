package directivestore

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/store/teams"
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

func newTestStore(t *testing.T, client *mongo.Client, teamID string) *Store {
	t.Helper()
	store, err := NewStore(client, teamID)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	return store
}

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

// ------------------------------------------------------------------
// Team CRUD tests
// ------------------------------------------------------------------

func TestStore_TeamCreateAndGet(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "")

	team := makeMinimalTeam("team_001", "Backend Squad")
	res, err := store.CreateTeam(ctx, team)
	if err != nil {
		t.Fatalf("CreateTeam failed: %v", err)
	}
	if res.InsertedID == nil {
		t.Fatal("expected InsertedID")
	}

	got, err := store.GetTeam(ctx, "team_001")
	if err != nil {
		t.Fatalf("GetTeam failed: %v", err)
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
}

func TestStore_TeamUpdate(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "")

	team := makeMinimalTeam("team_upd", "Update me")
	_, err := store.CreateTeam(ctx, team)
	if err != nil {
		t.Fatalf("CreateTeam failed: %v", err)
	}

	ures, err := store.UpdateTeam(ctx, "team_upd", bson.M{
		"status": StatusActive,
		"name":   "Updated name",
	})
	if err != nil {
		t.Fatalf("UpdateTeam failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	got, err := store.GetTeam(ctx, "team_upd")
	if err != nil {
		t.Fatalf("GetTeam after update failed: %v", err)
	}
	if got.Status != StatusActive {
		t.Fatalf("expected status ACTIVE, got %s", got.Status)
	}
	if got.Name != "Updated name" {
		t.Fatalf("expected name 'Updated name', got %s", got.Name)
	}
}

func TestStore_TeamSoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "")

	team := makeMinimalTeam("team_del", "Delete me")
	_, err := store.CreateTeam(ctx, team)
	if err != nil {
		t.Fatalf("CreateTeam failed: %v", err)
	}

	dres, err := store.DeleteTeam(ctx, "team_del")
	if err != nil {
		t.Fatalf("DeleteTeam failed: %v", err)
	}
	if dres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", dres.ModifiedCount)
	}

	_, err = store.GetTeam(ctx, "team_del")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	got, err := store.GetTeamWithDeleted(ctx, "team_del")
	if err != nil {
		t.Fatalf("GetTeamWithDeleted failed: %v", err)
	}
	if !got.Deleted {
		t.Fatalf("expected deleted=true, got %v", got.Deleted)
	}
	if got.DeletedAt == nil {
		t.Fatal("expected deleted_at to be set")
	}
}

func TestStore_TeamRestore(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "")

	team := makeMinimalTeam("team_res", "Restore me")
	_, err := store.CreateTeam(ctx, team)
	if err != nil {
		t.Fatalf("CreateTeam failed: %v", err)
	}

	_, _ = store.DeleteTeam(ctx, "team_res")

	rres, err := store.RestoreTeam(ctx, "team_res")
	if err != nil {
		t.Fatalf("RestoreTeam failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on restore, got %d", rres.ModifiedCount)
	}

	got, err := store.GetTeam(ctx, "team_res")
	if err != nil {
		t.Fatalf("GetTeam after restore failed: %v", err)
	}
	if got.Deleted {
		t.Fatalf("expected deleted=false after restore, got %v", got.Deleted)
	}
	if got.DeletedAt != nil {
		t.Fatalf("expected deleted_at nil after restore, got %v", got.DeletedAt)
	}
}

func TestStore_TeamHardDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "")

	team := makeMinimalTeam("team_hard", "Hard delete me")
	_, err := store.CreateTeam(ctx, team)
	if err != nil {
		t.Fatalf("CreateTeam failed: %v", err)
	}

	dres, err := store.HardDeleteTeam(ctx, "team_hard")
	if err != nil {
		t.Fatalf("HardDeleteTeam failed: %v", err)
	}
	if dres.DeletedCount != 1 {
		t.Fatalf("expected DeletedCount=1, got %d", dres.DeletedCount)
	}

	_, err = store.GetTeamWithDeleted(ctx, "team_hard")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

func TestStore_TeamList(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "")

	teams := []*Team{
		makeMinimalTeam("team_l1", "Team one"),
		makeMinimalTeam("team_l2", "Team two"),
		makeMinimalTeam("team_l3", "Team three"),
	}
	teams[0].Status = StatusForming
	teams[1].Status = StatusActive
	teams[2].Status = StatusForming

	for _, team := range teams {
		_, err := store.CreateTeam(ctx, team)
		if err != nil {
			t.Fatalf("CreateTeam failed: %v", err)
		}
	}

	all, err := store.ListTeams(ctx, TeamListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListTeams all failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 teams, got %d", len(all))
	}

	forming, err := store.ListTeams(ctx, TeamListOptions{Status: []TeamStatus{StatusForming}})
	if err != nil {
		t.Fatalf("ListTeams forming failed: %v", err)
	}
	if len(forming) != 2 {
		t.Fatalf("expected 2 forming teams, got %d", len(forming))
	}
}

func TestStore_TeamAddRemoveMember(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "")

	team := makeMinimalTeam("team_mem", "Member test")
	_, err := store.CreateTeam(ctx, team)
	if err != nil {
		t.Fatalf("CreateTeam failed: %v", err)
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

	got, err := store.GetTeam(ctx, "team_mem")
	if err != nil {
		t.Fatalf("GetTeam after add failed: %v", err)
	}
	if len(got.Members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(got.Members))
	}
	if got.Members[0].Agent != "silas" {
		t.Fatalf("expected member agent silas, got %s", got.Members[0].Agent)
	}

	rres, err := store.RemoveMember(ctx, "team_mem", "silas")
	if err != nil {
		t.Fatalf("RemoveMember failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on remove, got %d", rres.ModifiedCount)
	}

	got, err = store.GetTeam(ctx, "team_mem")
	if err != nil {
		t.Fatalf("GetTeam after remove failed: %v", err)
	}
	if len(got.Members) != 0 {
		t.Fatalf("expected 0 members after remove, got %d", len(got.Members))
	}
}

// ------------------------------------------------------------------
// Directive CRUD tests
// ------------------------------------------------------------------

func TestStore_DirectiveCreateAndGet(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_a")

	d := makeMinimalDirective("dir_001", "team_a", "Grow the brand")
	res, err := store.CreateDirective(ctx, d)
	if err != nil {
		t.Fatalf("CreateDirective failed: %v", err)
	}
	if res.InsertedID == nil {
		t.Fatal("expected InsertedID")
	}

	got, err := store.GetDirective(ctx, "dir_001")
	if err != nil {
		t.Fatalf("GetDirective failed: %v", err)
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
}

func TestStore_DirectiveUpdate(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_c")

	d := makeMinimalDirective("dir_upd", "team_c", "Update me")
	_, err := store.CreateDirective(ctx, d)
	if err != nil {
		t.Fatalf("CreateDirective failed: %v", err)
	}

	ures, err := store.UpdateDirective(ctx, "dir_upd", bson.M{
		"statement": "Updated statement",
		"version":   2,
		"status":    DirectiveRevised,
	})
	if err != nil {
		t.Fatalf("UpdateDirective failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	got, err := store.GetDirective(ctx, "dir_upd")
	if err != nil {
		t.Fatalf("GetDirective after update failed: %v", err)
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

func TestStore_DirectiveSoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_d")

	d := makeMinimalDirective("dir_del", "team_d", "Delete me")
	_, err := store.CreateDirective(ctx, d)
	if err != nil {
		t.Fatalf("CreateDirective failed: %v", err)
	}

	dres, err := store.DeleteDirective(ctx, "dir_del")
	if err != nil {
		t.Fatalf("DeleteDirective failed: %v", err)
	}
	if dres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", dres.ModifiedCount)
	}

	_, err = store.GetDirective(ctx, "dir_del")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	got, err := store.GetDirectiveWithDeleted(ctx, "dir_del")
	if err != nil {
		t.Fatalf("GetDirectiveWithDeleted failed: %v", err)
	}
	if !got.Deleted {
		t.Fatalf("expected deleted=true, got %v", got.Deleted)
	}
}

func TestStore_DirectiveRestore(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_e")

	d := makeMinimalDirective("dir_res", "team_e", "Restore me")
	_, err := store.CreateDirective(ctx, d)
	if err != nil {
		t.Fatalf("CreateDirective failed: %v", err)
	}

	_, _ = store.DeleteDirective(ctx, "dir_res")

	rres, err := store.RestoreDirective(ctx, "dir_res")
	if err != nil {
		t.Fatalf("RestoreDirective failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on restore, got %d", rres.ModifiedCount)
	}

	got, err := store.GetDirective(ctx, "dir_res")
	if err != nil {
		t.Fatalf("GetDirective after restore failed: %v", err)
	}
	if got.Deleted {
		t.Fatalf("expected deleted=false after restore, got %v", got.Deleted)
	}
}

func TestStore_DirectiveHardDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_f")

	d := makeMinimalDirective("dir_hard", "team_f", "Hard delete me")
	_, err := store.CreateDirective(ctx, d)
	if err != nil {
		t.Fatalf("CreateDirective failed: %v", err)
	}

	dres, err := store.HardDeleteDirective(ctx, "dir_hard")
	if err != nil {
		t.Fatalf("HardDeleteDirective failed: %v", err)
	}
	if dres.DeletedCount != 1 {
		t.Fatalf("expected DeletedCount=1, got %d", dres.DeletedCount)
	}

	_, err = store.GetDirectiveWithDeleted(ctx, "dir_hard")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

func TestStore_DirectiveList(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_g")

	ds := []*Directive{
		makeMinimalDirective("dir_l1", "team_g", "Directive one"),
		makeMinimalDirective("dir_l2", "team_g", "Directive two"),
		makeMinimalDirective("dir_l3", "team_h", "Directive three"),
	}
	ds[1].Status = DirectiveRetired

	for _, d := range ds {
		_, err := store.CreateDirective(ctx, d)
		if err != nil {
			t.Fatalf("CreateDirective failed: %v", err)
		}
	}

	all, err := store.ListDirectives(ctx, teams.DirectiveListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListDirectives all failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 directives, got %d", len(all))
	}

	byTeam, err := store.ListDirectives(ctx, teams.DirectiveListOptions{TeamID: "team_g"})
	if err != nil {
		t.Fatalf("ListDirectives by team failed: %v", err)
	}
	if len(byTeam) != 2 {
		t.Fatalf("expected 2 directives for team_g, got %d", len(byTeam))
	}

	activeOnly, err := store.ListDirectives(ctx, teams.DirectiveListOptions{Status: []teams.DirectiveStatus{DirectiveActive}})
	if err != nil {
		t.Fatalf("ListDirectives active failed: %v", err)
	}
	if len(activeOnly) != 2 {
		t.Fatalf("expected 2 active directives, got %d", len(activeOnly))
	}
}

// ------------------------------------------------------------------
// Initiative CRUD tests
// ------------------------------------------------------------------

func TestStore_InitiativeCreateAndGet(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_a")

	in := makeMinimalInitiative("init_001", "dir_001", "team_a", "Grow Instagram")
	res, err := store.CreateInitiative(ctx, in)
	if err != nil {
		t.Fatalf("CreateInitiative failed: %v", err)
	}
	if res.InsertedID == nil {
		t.Fatal("expected InsertedID")
	}

	got, err := store.GetInitiative(ctx, "init_001")
	if err != nil {
		t.Fatalf("GetInitiative failed: %v", err)
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
}

func TestStore_InitiativeUpdate(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_c")

	in := makeMinimalInitiative("init_upd", "dir_003", "team_c", "Update me")
	_, err := store.CreateInitiative(ctx, in)
	if err != nil {
		t.Fatalf("CreateInitiative failed: %v", err)
	}

	ures, err := store.UpdateInitiative(ctx, "init_upd", bson.M{
		"status":      InitiativeActive,
		"title":       "Updated title",
		"description": "Updated description",
	})
	if err != nil {
		t.Fatalf("UpdateInitiative failed: %v", err)
	}
	if ures.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", ures.ModifiedCount)
	}

	got, err := store.GetInitiative(ctx, "init_upd")
	if err != nil {
		t.Fatalf("GetInitiative after update failed: %v", err)
	}
	if got.Status != InitiativeActive {
		t.Fatalf("expected status ACTIVE, got %s", got.Status)
	}
	if got.Title != "Updated title" {
		t.Fatalf("expected title 'Updated title', got %s", got.Title)
	}
}

func TestStore_InitiativeSoftDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_d")

	in := makeMinimalInitiative("init_del", "dir_004", "team_d", "Delete me")
	_, err := store.CreateInitiative(ctx, in)
	if err != nil {
		t.Fatalf("CreateInitiative failed: %v", err)
	}

	dres, err := store.DeleteInitiative(ctx, "init_del")
	if err != nil {
		t.Fatalf("DeleteInitiative failed: %v", err)
	}
	if dres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", dres.ModifiedCount)
	}

	_, err = store.GetInitiative(ctx, "init_del")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	got, err := store.GetInitiativeWithDeleted(ctx, "init_del")
	if err != nil {
		t.Fatalf("GetInitiativeWithDeleted failed: %v", err)
	}
	if !got.Deleted {
		t.Fatalf("expected deleted=true, got %v", got.Deleted)
	}
}

func TestStore_InitiativeRestore(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_e")

	in := makeMinimalInitiative("init_res", "dir_005", "team_e", "Restore me")
	_, err := store.CreateInitiative(ctx, in)
	if err != nil {
		t.Fatalf("CreateInitiative failed: %v", err)
	}

	_, _ = store.DeleteInitiative(ctx, "init_res")

	rres, err := store.RestoreInitiative(ctx, "init_res")
	if err != nil {
		t.Fatalf("RestoreInitiative failed: %v", err)
	}
	if rres.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on restore, got %d", rres.ModifiedCount)
	}

	got, err := store.GetInitiative(ctx, "init_res")
	if err != nil {
		t.Fatalf("GetInitiative after restore failed: %v", err)
	}
	if got.Deleted {
		t.Fatalf("expected deleted=false after restore, got %v", got.Deleted)
	}
}

func TestStore_InitiativeHardDelete(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_f")

	in := makeMinimalInitiative("init_hard", "dir_006", "team_f", "Hard delete me")
	_, err := store.CreateInitiative(ctx, in)
	if err != nil {
		t.Fatalf("CreateInitiative failed: %v", err)
	}

	dres, err := store.HardDeleteInitiative(ctx, "init_hard")
	if err != nil {
		t.Fatalf("HardDeleteInitiative failed: %v", err)
	}
	if dres.DeletedCount != 1 {
		t.Fatalf("expected DeletedCount=1, got %d", dres.DeletedCount)
	}

	_, err = store.GetInitiativeWithDeleted(ctx, "init_hard")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after hard delete, got %v", err)
	}
}

func TestStore_InitiativeList(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_g")

	ins := []*Initiative{
		makeMinimalInitiative("init_l1", "dir_007", "team_g", "Initiative one"),
		makeMinimalInitiative("init_l2", "dir_007", "team_g", "Initiative two"),
		makeMinimalInitiative("init_l3", "dir_008", "team_h", "Initiative three"),
	}
	ins[1].Status = InitiativeActive

	for _, in := range ins {
		_, err := store.CreateInitiative(ctx, in)
		if err != nil {
			t.Fatalf("CreateInitiative failed: %v", err)
		}
	}

	all, err := store.ListInitiatives(ctx, teams.InitiativeListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListInitiatives all failed: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 initiatives, got %d", len(all))
	}

	byTeam, err := store.ListInitiatives(ctx, teams.InitiativeListOptions{TeamID: "team_g"})
	if err != nil {
		t.Fatalf("ListInitiatives by team failed: %v", err)
	}
	if len(byTeam) != 2 {
		t.Fatalf("expected 2 initiatives for team_g, got %d", len(byTeam))
	}

	byDirective, err := store.ListInitiatives(ctx, teams.InitiativeListOptions{DirectiveID: "dir_007"})
	if err != nil {
		t.Fatalf("ListInitiatives by directive failed: %v", err)
	}
	if len(byDirective) != 2 {
		t.Fatalf("expected 2 initiatives for dir_007, got %d", len(byDirective))
	}

	activeOnly, err := store.ListInitiatives(ctx, teams.InitiativeListOptions{Status: []teams.InitiativeStatus{InitiativeActive}})
	if err != nil {
		t.Fatalf("ListInitiatives active failed: %v", err)
	}
	if len(activeOnly) != 1 {
		t.Fatalf("expected 1 active initiative, got %d", len(activeOnly))
	}
}

// ------------------------------------------------------------------
// AddPlanToInitiative tests
// ------------------------------------------------------------------

func TestStore_AddPlanToInitiative(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_k")

	in := makeMinimalInitiative("init_plan", "dir_010", "team_k", "Plan linkage test")
	_, err := store.CreateInitiative(ctx, in)
	if err != nil {
		t.Fatalf("CreateInitiative failed: %v", err)
	}

	res, err := store.AddPlanToInitiative(ctx, "init_plan", "plan_001")
	if err != nil {
		t.Fatalf("AddPlanToInitiative failed: %v", err)
	}
	if res.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", res.ModifiedCount)
	}

	got, err := store.GetInitiative(ctx, "init_plan")
	if err != nil {
		t.Fatalf("GetInitiative after add failed: %v", err)
	}
	if len(got.PlanIDs) != 1 {
		t.Fatalf("expected 1 plan_id, got %d", len(got.PlanIDs))
	}
	if got.PlanIDs[0] != "plan_001" {
		t.Fatalf("expected plan_id plan_001, got %s", got.PlanIDs[0])
	}

	// Idempotent add
	res2, err := store.AddPlanToInitiative(ctx, "init_plan", "plan_001")
	if err != nil {
		t.Fatalf("AddPlanToInitiative idempotent failed: %v", err)
	}
	_ = res2

	got2, err := store.GetInitiative(ctx, "init_plan")
	if err != nil {
		t.Fatalf("GetInitiative after idempotent add failed: %v", err)
	}
	if len(got2.PlanIDs) != 1 {
		t.Fatalf("expected still 1 plan_id after idempotent add, got %d", len(got2.PlanIDs))
	}

	// Add second plan
	res3, err := store.AddPlanToInitiative(ctx, "init_plan", "plan_002")
	if err != nil {
		t.Fatalf("AddPlanToInitiative second plan failed: %v", err)
	}
	if res3.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1 on second plan, got %d", res3.ModifiedCount)
	}

	got3, err := store.GetInitiative(ctx, "init_plan")
	if err != nil {
		t.Fatalf("GetInitiative after second add failed: %v", err)
	}
	if len(got3.PlanIDs) != 2 {
		t.Fatalf("expected 2 plan_ids, got %d", len(got3.PlanIDs))
	}
}

func TestStore_AddPlanToInitiative_Bidirectional(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_l")
	planColl := client.Database("team_l").Collection("plans")

	in := makeMinimalInitiative("init_bidir", "dir_011", "team_l", "Bidirectional test")
	_, err := store.CreateInitiative(ctx, in)
	if err != nil {
		t.Fatalf("CreateInitiative failed: %v", err)
	}

	// Seed a plan document
	_, err = planColl.InsertOne(ctx, bson.M{
		"plan_id":    "plan_bidir",
		"title":      "Bidirectional plan",
		"status":     "PLANNING",
		"created_at": time.Now().UTC(),
		"updated_at": time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Plan insert failed: %v", err)
	}

	// Add plan to initiative with bidirectional update using explicit collection
	res, err := store.AddPlanToInitiativeWithColl(ctx, "init_bidir", "plan_bidir", planColl)
	if err != nil {
		t.Fatalf("AddPlanToInitiativeWithColl failed: %v", err)
	}
	if res.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", res.ModifiedCount)
	}

	// Verify initiative side
	gotIn, err := store.GetInitiative(ctx, "init_bidir")
	if err != nil {
		t.Fatalf("GetInitiative failed: %v", err)
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

func TestStore_RemovePlanFromInitiative(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "team_m")

	in := makeMinimalInitiative("init_rm", "dir_012", "team_m", "Remove plan test")
	in.PlanIDs = []string{"plan_001", "plan_002"}
	_, err := store.CreateInitiative(ctx, in)
	if err != nil {
		t.Fatalf("CreateInitiative failed: %v", err)
	}

	res, err := store.RemovePlanFromInitiative(ctx, "init_rm", "plan_001")
	if err != nil {
		t.Fatalf("RemovePlanFromInitiative failed: %v", err)
	}
	if res.ModifiedCount != 1 {
		t.Fatalf("expected ModifiedCount=1, got %d", res.ModifiedCount)
	}

	got, err := store.GetInitiative(ctx, "init_rm")
	if err != nil {
		t.Fatalf("GetInitiative after remove failed: %v", err)
	}
	if len(got.PlanIDs) != 1 {
		t.Fatalf("expected 1 plan_id after remove, got %d", len(got.PlanIDs))
	}
	if got.PlanIDs[0] != "plan_002" {
		t.Fatalf("expected remaining plan_id plan_002, got %s", got.PlanIDs[0])
	}
}

// ------------------------------------------------------------------
// Full round-trip test
// ------------------------------------------------------------------

func TestStore_FullRoundTrip(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client, "round_trip")

	// 1. Create team
	team := makeMinimalTeam("team_round", "Round-trip team")
	team.Status = StatusActive
	_, err := store.CreateTeam(ctx, team)
	if err != nil {
		t.Fatalf("CreateTeam failed: %v", err)
	}

	// 2. Create directive
	d := &Directive{
		DirectiveID: "dir_round",
		TeamID:      "team_round",
		Statement:   "Build a sustainable open-source community",
		SuccessCriteria: []SuccessCriterion{
			{Metric: "contributor_count", Target: "50", Unit: "people"},
		},
		Status:    DirectiveActive,
		Version:   1,
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	_, err = store.CreateDirective(ctx, d)
	if err != nil {
		t.Fatalf("CreateDirective failed: %v", err)
	}

	// 3. Create initiative
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
		PlanIDs:   []string{},
		Status:    InitiativeActive,
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
		UpdatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	_, err = store.CreateInitiative(ctx, in)
	if err != nil {
		t.Fatalf("CreateInitiative failed: %v", err)
	}

	// 4. Add plan to initiative
	planColl := client.Database("team_round_trip").Collection("plans")
	_, err = planColl.InsertOne(ctx, bson.M{
		"plan_id":    "plan_round",
		"title":      "Round-trip plan",
		"status":     "PLANNING",
		"created_at": time.Now().UTC(),
		"updated_at": time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Plan insert failed: %v", err)
	}

	_, err = store.AddPlanToInitiativeWithColl(ctx, "init_round", "plan_round", planColl)
	if err != nil {
		t.Fatalf("AddPlanToInitiativeWithColl failed: %v", err)
	}

	// 5. Verify initiative has plan
	gotIn, err := store.GetInitiative(ctx, "init_round")
	if err != nil {
		t.Fatalf("GetInitiative failed: %v", err)
	}
	if len(gotIn.PlanIDs) != 1 || gotIn.PlanIDs[0] != "plan_round" {
		t.Fatalf("expected plan_id plan_round in initiative, got %v", gotIn.PlanIDs)
	}

	// 6. Verify plan has initiative_id
	var planDoc bson.M
	err = planColl.FindOne(ctx, bson.M{"plan_id": "plan_round"}).Decode(&planDoc)
	if err != nil {
		t.Fatalf("Find plan failed: %v", err)
	}
	if planDoc["initiative_id"] != "init_round" {
		t.Fatalf("expected initiative_id init_round on plan, got %v", planDoc["initiative_id"])
	}

	// 7. Soft delete initiative
	_, err = store.DeleteInitiative(ctx, "init_round")
	if err != nil {
		t.Fatalf("DeleteInitiative failed: %v", err)
	}

	_, err = store.GetInitiative(ctx, "init_round")
	if err != mongo.ErrNoDocuments {
		t.Fatalf("expected ErrNoDocuments after soft delete, got %v", err)
	}

	// 8. Restore initiative
	_, err = store.RestoreInitiative(ctx, "init_round")
	if err != nil {
		t.Fatalf("RestoreInitiative failed: %v", err)
	}

	restored, err := store.GetInitiative(ctx, "init_round")
	if err != nil {
		t.Fatalf("GetInitiative after restore failed: %v", err)
	}
	if restored.Deleted {
		t.Fatal("expected deleted=false after restore")
	}
	if restored.Status != InitiativeActive {
		t.Fatalf("expected status preserved as ACTIVE after restore, got %s", restored.Status)
	}
}

func strPtr(s string) *string {
	return &s
}
