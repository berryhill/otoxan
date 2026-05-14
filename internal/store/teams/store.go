// Package teams provides a MongoDB-backed store for team documents with
// soft-delete semantics.
package teams

import (
	"context"
	"fmt"
	"time"

	"github.com/silas/otoxan/internal/softdelete"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// TeamStore
// ------------------------------------------------------------------

// NewTeamStore creates a TeamStore backed by the given MongoDB collection.
// It ensures required indexes on the teams collection.
func NewTeamStore(coll *mongo.Collection) *TeamStore {
	sd := softdelete.NewSoftDelete(coll)
	ts := &TeamStore{sd: sd}
	_ = ts.ensureIndexes(context.Background())
	return ts
}

// TeamStore is a MongoDB-backed store for team documents with soft-delete
// semantics. It mirrors the Python teamstore.TeamStore CRUD surface.
type TeamStore struct {
	sd *softdelete.SoftDeleteCollection
}

// ensureIndexes creates required indexes for the teams collection.
func (s *TeamStore) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "team_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}}},
	}
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// Create inserts a new team document. The caller must set TeamID and Name.
func (s *TeamStore) Create(ctx context.Context, team *Team) (*mongo.InsertOneResult, error) {
	now := time.Now().UTC()
	if team.CreatedAt.IsZero() {
		team.CreatedAt = now
	}
	if team.UpdatedAt.IsZero() {
		team.UpdatedAt = now
	}
	if team.Status == "" {
		team.Status = StatusForming
	}
	if team.DBName == "" {
		team.DBName = fmt.Sprintf("team_%s", team.TeamID)
	}
	if team.Members == nil {
		team.Members = []Member{}
	}
	if team.Artifacts == nil {
		team.Artifacts = map[string]interface{}{}
	}
	return s.sd.InsertOne(ctx, team)
}

// Get retrieves a single team by team_id. Returns mongo.ErrNoDocuments if
// not found (or soft-deleted).
func (s *TeamStore) Get(ctx context.Context, teamID string) (*Team, error) {
	sr := s.sd.FindOne(ctx, bson.M{"team_id": teamID})
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var t Team
	if err := sr.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// GetWithDeleted retrieves a team including soft-deleted ones.
func (s *TeamStore) GetWithDeleted(ctx context.Context, teamID string) (*Team, error) {
	sr := s.sd.FindOne(ctx, bson.M{"team_id": teamID}, softdelete.WithIncludeDeleted())
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var t Team
	if err := sr.Decode(&t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Update patches fields of an existing team. Sets updated_at automatically.
func (s *TeamStore) Update(ctx context.Context, teamID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"team_id": teamID}, bson.M{"$set": updates})
}

// Delete soft-deletes a team by team_id.
func (s *TeamStore) Delete(ctx context.Context, teamID string) (*mongo.UpdateResult, error) {
	return s.sd.Delete(ctx, bson.M{"team_id": teamID})
}

// Restore un-deletes a soft-deleted team.
func (s *TeamStore) Restore(ctx context.Context, teamID string) (*mongo.UpdateResult, error) {
	return s.sd.Restore(ctx, bson.M{"team_id": teamID})
}

// HardDelete permanently removes a team.
func (s *TeamStore) HardDelete(ctx context.Context, teamID string) (*mongo.DeleteResult, error) {
	return s.sd.HardDelete(ctx, bson.M{"team_id": teamID})
}

// TeamListOptions configures the List query for teams.
type TeamListOptions struct {
	Status         []TeamStatus
	Limit          int
	IncludeDeleted bool
}

// List returns teams matching the provided filters, sorted by created_at descending.
func (s *TeamStore) List(ctx context.Context, opts TeamListOptions) ([]Team, error) {
	filter := bson.M{}
	if len(opts.Status) > 0 {
		if len(opts.Status) == 1 {
			filter["status"] = opts.Status[0]
		} else {
			statuses := make([]string, len(opts.Status))
			for i, st := range opts.Status {
				statuses[i] = string(st)
			}
			filter["status"] = bson.M{"$in": statuses}
		}
	}

	var sdOpts []softdelete.Option
	if opts.IncludeDeleted {
		sdOpts = append(sdOpts, softdelete.WithIncludeDeleted())
	}

	cur, err := s.sd.Find(ctx, filter, sdOpts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var teams []Team
	if err := cur.All(ctx, &teams); err != nil {
		return nil, err
	}

	// Sort by created_at desc, limit in-memory
	sortTeams(teams)
	if opts.Limit > 0 && len(teams) > opts.Limit {
		teams = teams[:opts.Limit]
	}
	return teams, nil
}

// Count returns the number of teams matching the filter.
func (s *TeamStore) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.sd.CountDocuments(ctx, filter)
}

// AddMember pushes a member into the team's members array.
func (s *TeamStore) AddMember(ctx context.Context, teamID string, member Member) (*mongo.UpdateResult, error) {
	member.JoinedAt = time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"team_id": teamID}, bson.M{
		"$push": bson.M{"members": member},
		"$set":  bson.M{"updated_at": time.Now().UTC()},
	})
}

// RemoveMember pulls a member by agent name from the team's members array.
func (s *TeamStore) RemoveMember(ctx context.Context, teamID string, agent string) (*mongo.UpdateResult, error) {
	return s.sd.UpdateOne(ctx, bson.M{"team_id": teamID}, bson.M{
		"$pull": bson.M{"members": bson.M{"agent": agent}},
		"$set":  bson.M{"updated_at": time.Now().UTC()},
	})
}

// sortTeams sorts by created_at descending.
func sortTeams(teams []Team) {
	for i := 0; i < len(teams); i++ {
		for j := i + 1; j < len(teams); j++ {
			if teams[j].CreatedAt.After(teams[i].CreatedAt) {
				teams[i], teams[j] = teams[j], teams[i]
			}
		}
	}
}

// Name returns the underlying collection name.
func (s *TeamStore) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *TeamStore) Database() *mongo.Database {
	return s.sd.Database()
}

// ------------------------------------------------------------------
// DirectiveStore (team-scoped directives)
// ------------------------------------------------------------------

// NewDirectiveStore creates a DirectiveStore backed by the given MongoDB collection.
// It ensures required indexes on the directives collection.
func NewDirectiveStore(coll *mongo.Collection) *DirectiveStore {
	sd := softdelete.NewSoftDelete(coll)
	ds := &DirectiveStore{sd: sd}
	_ = ds.ensureIndexes(context.Background())
	return ds
}

// DirectiveStore is a MongoDB-backed store for team directive documents with soft-delete
// semantics. It mirrors the Python teamstore directive CRUD surface.
type DirectiveStore struct {
	sd *softdelete.SoftDeleteCollection
}

// ensureIndexes creates required indexes for the directives collection.
func (s *DirectiveStore) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "directive_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "team_id", Value: 1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
	}
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// CreateDirective inserts a new team directive document.
func (s *DirectiveStore) Create(ctx context.Context, d *Directive) (*mongo.InsertOneResult, error) {
	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = now
	}
	if d.Status == "" {
		d.Status = DirectiveActive
	}
	if d.Version == 0 {
		d.Version = 1
	}
	if d.SuccessCriteria == nil {
		d.SuccessCriteria = []SuccessCriterion{}
	}
	return s.sd.InsertOne(ctx, d)
}

// GetDirective retrieves a single directive by directive_id.
func (s *DirectiveStore) Get(ctx context.Context, directiveID string) (*Directive, error) {
	sr := s.sd.FindOne(ctx, bson.M{"directive_id": directiveID})
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var d Directive
	if err := sr.Decode(&d); err != nil {
		return nil, err
	}
	return &d, nil
}

// GetDirectiveWithDeleted retrieves a directive including soft-deleted ones.
func (s *DirectiveStore) GetWithDeleted(ctx context.Context, directiveID string) (*Directive, error) {
	sr := s.sd.FindOne(ctx, bson.M{"directive_id": directiveID}, softdelete.WithIncludeDeleted())
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var d Directive
	if err := sr.Decode(&d); err != nil {
		return nil, err
	}
	return &d, nil
}

// UpdateDirective patches fields of an existing directive. Sets updated_at automatically.
func (s *DirectiveStore) Update(ctx context.Context, directiveID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"directive_id": directiveID}, bson.M{"$set": updates})
}

// DeleteDirective soft-deletes a directive by directive_id.
func (s *DirectiveStore) Delete(ctx context.Context, directiveID string) (*mongo.UpdateResult, error) {
	return s.sd.Delete(ctx, bson.M{"directive_id": directiveID})
}

// RestoreDirective un-deletes a soft-deleted directive.
func (s *DirectiveStore) Restore(ctx context.Context, directiveID string) (*mongo.UpdateResult, error) {
	return s.sd.Restore(ctx, bson.M{"directive_id": directiveID})
}

// HardDeleteDirective permanently removes a directive.
func (s *DirectiveStore) HardDelete(ctx context.Context, directiveID string) (*mongo.DeleteResult, error) {
	return s.sd.HardDelete(ctx, bson.M{"directive_id": directiveID})
}

// DirectiveListOptions configures the List query for directives.
type DirectiveListOptions struct {
	TeamID         string
	Status         []DirectiveStatus
	Limit          int
	IncludeDeleted bool
}

// ListDirectives returns directives matching the provided filters, sorted by created_at descending.
func (s *DirectiveStore) List(ctx context.Context, opts DirectiveListOptions) ([]Directive, error) {
	filter := bson.M{}
	if opts.TeamID != "" {
		filter["team_id"] = opts.TeamID
	}
	if len(opts.Status) > 0 {
		if len(opts.Status) == 1 {
			filter["status"] = opts.Status[0]
		} else {
			statuses := make([]string, len(opts.Status))
			for i, st := range opts.Status {
				statuses[i] = string(st)
			}
			filter["status"] = bson.M{"$in": statuses}
		}
	}

	var sdOpts []softdelete.Option
	if opts.IncludeDeleted {
		sdOpts = append(sdOpts, softdelete.WithIncludeDeleted())
	}

	cur, err := s.sd.Find(ctx, filter, sdOpts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var directives []Directive
	if err := cur.All(ctx, &directives); err != nil {
		return nil, err
	}

	// Sort by created_at desc, limit in-memory
	sortDirectives(directives)
	if opts.Limit > 0 && len(directives) > opts.Limit {
		directives = directives[:opts.Limit]
	}
	return directives, nil
}

// CountDirectives returns the number of directives matching the filter.
func (s *DirectiveStore) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.sd.CountDocuments(ctx, filter)
}

// sortDirectives sorts by created_at descending.
func sortDirectives(directives []Directive) {
	for i := 0; i < len(directives); i++ {
		for j := i + 1; j < len(directives); j++ {
			if directives[j].CreatedAt.After(directives[i].CreatedAt) {
				directives[i], directives[j] = directives[j], directives[i]
			}
		}
	}
}

// Name returns the underlying collection name.
func (s *DirectiveStore) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *DirectiveStore) Database() *mongo.Database {
	return s.sd.Database()
}

// ------------------------------------------------------------------
// InitiativeStore (team-scoped initiatives)
// ------------------------------------------------------------------

// NewInitiativeStore creates an InitiativeStore backed by the given MongoDB collection.
// It ensures required indexes on the initiatives collection.
func NewInitiativeStore(coll *mongo.Collection) *InitiativeStore {
	sd := softdelete.NewSoftDelete(coll)
	is := &InitiativeStore{sd: sd}
	_ = is.ensureIndexes(context.Background())
	return is
}

// InitiativeStore is a MongoDB-backed store for team initiative documents with soft-delete
// semantics. It mirrors the Python teamstore initiative CRUD surface.
type InitiativeStore struct {
	sd *softdelete.SoftDeleteCollection
}

// ensureIndexes creates required indexes for the initiatives collection.
func (s *InitiativeStore) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "initiative_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "team_id", Value: 1}}},
		{Keys: bson.D{{Key: "directive_id", Value: 1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
	}
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// CreateInitiative inserts a new initiative document.
func (s *InitiativeStore) Create(ctx context.Context, in *Initiative) (*mongo.InsertOneResult, error) {
	now := time.Now().UTC()
	if in.CreatedAt.IsZero() {
		in.CreatedAt = now
	}
	if in.UpdatedAt.IsZero() {
		in.UpdatedAt = now
	}
	if in.Status == "" {
		in.Status = InitiativeProposed
	}
	if in.PlanIDs == nil {
		in.PlanIDs = []string{}
	}
	if in.SuccessCriteria == nil {
		in.SuccessCriteria = []SuccessCriterion{}
	}
	return s.sd.InsertOne(ctx, in)
}

// GetInitiative retrieves a single initiative by initiative_id.
func (s *InitiativeStore) Get(ctx context.Context, initiativeID string) (*Initiative, error) {
	sr := s.sd.FindOne(ctx, bson.M{"initiative_id": initiativeID})
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var in Initiative
	if err := sr.Decode(&in); err != nil {
		return nil, err
	}
	return &in, nil
}

// GetInitiativeWithDeleted retrieves an initiative including soft-deleted ones.
func (s *InitiativeStore) GetWithDeleted(ctx context.Context, initiativeID string) (*Initiative, error) {
	sr := s.sd.FindOne(ctx, bson.M{"initiative_id": initiativeID}, softdelete.WithIncludeDeleted())
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var in Initiative
	if err := sr.Decode(&in); err != nil {
		return nil, err
	}
	return &in, nil
}

// UpdateInitiative patches fields of an existing initiative. Sets updated_at automatically.
func (s *InitiativeStore) Update(ctx context.Context, initiativeID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"initiative_id": initiativeID}, bson.M{"$set": updates})
}

// DeleteInitiative soft-deletes an initiative by initiative_id.
func (s *InitiativeStore) Delete(ctx context.Context, initiativeID string) (*mongo.UpdateResult, error) {
	return s.sd.Delete(ctx, bson.M{"initiative_id": initiativeID})
}

// RestoreInitiative un-deletes a soft-deleted initiative.
func (s *InitiativeStore) Restore(ctx context.Context, initiativeID string) (*mongo.UpdateResult, error) {
	return s.sd.Restore(ctx, bson.M{"initiative_id": initiativeID})
}

// HardDeleteInitiative permanently removes an initiative.
func (s *InitiativeStore) HardDelete(ctx context.Context, initiativeID string) (*mongo.DeleteResult, error) {
	return s.sd.HardDelete(ctx, bson.M{"initiative_id": initiativeID})
}

// InitiativeListOptions configures the List query for initiatives.
type InitiativeListOptions struct {
	TeamID         string
	DirectiveID    string
	Status         []InitiativeStatus
	Limit          int
	IncludeDeleted bool
}

// ListInitiatives returns initiatives matching the provided filters, sorted by created_at descending.
func (s *InitiativeStore) List(ctx context.Context, opts InitiativeListOptions) ([]Initiative, error) {
	filter := bson.M{}
	if opts.TeamID != "" {
		filter["team_id"] = opts.TeamID
	}
	if opts.DirectiveID != "" {
		filter["directive_id"] = opts.DirectiveID
	}
	if len(opts.Status) > 0 {
		if len(opts.Status) == 1 {
			filter["status"] = opts.Status[0]
		} else {
			statuses := make([]string, len(opts.Status))
			for i, st := range opts.Status {
				statuses[i] = string(st)
			}
			filter["status"] = bson.M{"$in": statuses}
		}
	}

	var sdOpts []softdelete.Option
	if opts.IncludeDeleted {
		sdOpts = append(sdOpts, softdelete.WithIncludeDeleted())
	}

	cur, err := s.sd.Find(ctx, filter, sdOpts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var initiatives []Initiative
	if err := cur.All(ctx, &initiatives); err != nil {
		return nil, err
	}

	// Sort by created_at desc, limit in-memory
	sortInitiatives(initiatives)
	if opts.Limit > 0 && len(initiatives) > opts.Limit {
		initiatives = initiatives[:opts.Limit]
	}
	return initiatives, nil
}

// CountInitiatives returns the number of initiatives matching the filter.
func (s *InitiativeStore) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.sd.CountDocuments(ctx, filter)
}

// AddPlanToInitiative links a plan to an initiative bidirectionally.
// It pushes the plan_id into the initiative's plan_ids array (via $addToSet)
// and also updates the plan document to set its initiative_id field.
// This mirrors teamstore.py:add_plan.
func (s *InitiativeStore) AddPlanToInitiative(ctx context.Context, initiativeID string, planID string, planColl *mongo.Collection) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()

	// 1. Add plan_id to initiative's plan_ids array
	res, err := s.sd.UpdateOne(ctx, bson.M{"initiative_id": initiativeID}, bson.M{
		"$addToSet": bson.M{"plan_ids": planID},
		"$set":      bson.M{"updated_at": now},
	})
	if err != nil {
		return nil, err
	}

	// 2. Bidirectional: set initiative_id on the plan document
	if res.MatchedCount > 0 && planColl != nil {
		_, _ = planColl.UpdateOne(ctx, bson.M{"plan_id": planID}, bson.M{
			"$set": bson.M{"initiative_id": initiativeID, "updated_at": now},
		})
	}

	return res, nil
}

// RemovePlanFromInitiative removes a plan_id from an initiative's plan_ids array.
func (s *InitiativeStore) RemovePlanFromInitiative(ctx context.Context, initiativeID string, planID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"initiative_id": initiativeID}, bson.M{
		"$pull": bson.M{"plan_ids": planID},
		"$set":  bson.M{"updated_at": now},
	})
}

// sortInitiatives sorts by created_at descending.
func sortInitiatives(initiatives []Initiative) {
	for i := 0; i < len(initiatives); i++ {
		for j := i + 1; j < len(initiatives); j++ {
			if initiatives[j].CreatedAt.After(initiatives[i].CreatedAt) {
				initiatives[i], initiatives[j] = initiatives[j], initiatives[i]
			}
		}
	}
}

// Name returns the underlying collection name.
func (s *InitiativeStore) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *InitiativeStore) Database() *mongo.Database {
	return s.sd.Database()
}
