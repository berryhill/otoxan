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

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (s *TeamStore) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "team_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}}},
	}
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// ------------------------------------------------------------------
// CRUD
// ------------------------------------------------------------------

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

// ------------------------------------------------------------------
// List / Query
// ------------------------------------------------------------------

// ListOptions configures the List query.
type ListOptions struct {
	Status         []TeamStatus
	Limit          int
	IncludeDeleted bool
}

// List returns teams matching the provided filters, sorted by created_at descending.
func (s *TeamStore) List(ctx context.Context, opts ListOptions) ([]Team, error) {
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

// ------------------------------------------------------------------
// Collection passthrough
// ------------------------------------------------------------------

// Name returns the underlying collection name.
func (s *TeamStore) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *TeamStore) Database() *mongo.Database {
	return s.sd.Database()
}
