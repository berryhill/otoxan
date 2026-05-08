// Package plans provides a MongoDB-backed store for plan documents with
// soft-delete semantics.
package plans

import (
	"context"
	"time"

	"github.com/silas/otoxan/internal/softdelete"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// NewPlanStore creates a PlanStore backed by the given MongoDB collection.
// It ensures required indexes on the plans collection.
func NewPlanStore(coll *mongo.Collection) *PlanStore {
	sd := softdelete.NewSoftDelete(coll)
	ps := &PlanStore{sd: sd}
	_ = ps.ensureIndexes(context.Background())
	return ps
}

// PlanStore is a MongoDB-backed store for plan documents with soft-delete
// semantics. It mirrors the Python planstore.PlanStore CRUD surface.
type PlanStore struct {
	sd *softdelete.SoftDeleteCollection
}

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (s *PlanStore) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "plan_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "updated_at", Value: 1}}},
		{Keys: bson.D{{Key: "archived_at", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "initiative_id", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "directive_id", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "team_id", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "flow_session_id", Value: 1}}, Options: options.Index().SetSparse(true)},
	}
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// ------------------------------------------------------------------
// CRUD
// ------------------------------------------------------------------

// Create inserts a new plan document. The caller must set PlanID and Title.
// Defaults are applied for optional fields.
func (s *PlanStore) Create(ctx context.Context, plan *Plan) (*mongo.InsertOneResult, error) {
	now := time.Now().UTC()
	if plan.CreatedAt.IsZero() {
		plan.CreatedAt = now
	}
	if plan.UpdatedAt.IsZero() {
		plan.UpdatedAt = now
	}
	if plan.Status == "" {
		plan.Status = StatusPlanning
	}
	if plan.PlanType == "" {
		plan.PlanType = TypeStandard
	}
	if plan.Tags == nil {
		plan.Tags = []string{}
	}
	return s.sd.InsertOne(ctx, plan)
}

// Get retrieves a single plan by plan_id. Returns mongo.ErrNoDocuments if
// not found (or soft-deleted).
func (s *PlanStore) Get(ctx context.Context, planID string) (*Plan, error) {
	sr := s.sd.FindOne(ctx, bson.M{"plan_id": planID})
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var p Plan
	if err := sr.Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetWithDeleted retrieves a plan including soft-deleted ones.
func (s *PlanStore) GetWithDeleted(ctx context.Context, planID string) (*Plan, error) {
	sr := s.sd.FindOne(ctx, bson.M{"plan_id": planID}, softdelete.WithIncludeDeleted())
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var p Plan
	if err := sr.Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Update patches fields of an existing plan. Sets updated_at automatically.
func (s *PlanStore) Update(ctx context.Context, planID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"plan_id": planID}, bson.M{"$set": updates})
}

// Delete soft-deletes a plan by plan_id.
func (s *PlanStore) Delete(ctx context.Context, planID string) (*mongo.UpdateResult, error) {
	return s.sd.Delete(ctx, bson.M{"plan_id": planID})
}

// Restore un-deletes a soft-deleted plan.
func (s *PlanStore) Restore(ctx context.Context, planID string) (*mongo.UpdateResult, error) {
	return s.sd.Restore(ctx, bson.M{"plan_id": planID})
}

// HardDelete permanently removes a plan.
func (s *PlanStore) HardDelete(ctx context.Context, planID string) (*mongo.DeleteResult, error) {
	return s.sd.HardDelete(ctx, bson.M{"plan_id": planID})
}

// ------------------------------------------------------------------
// List / Query
// ------------------------------------------------------------------

// ListOptions configures the List query.
type ListOptions struct {
	Status          []PlanStatus
	Tag             string
	Limit           int
	IncludeDeleted  bool
	IncludeArchived bool
}

// List returns plans matching the provided filters, sorted by updated_at descending.
func (s *PlanStore) List(ctx context.Context, opts ListOptions) ([]Plan, error) {
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
	if opts.Tag != "" {
		filter["tags"] = opts.Tag
	}
	if !opts.IncludeArchived {
		filter["archived_at"] = bson.M{"$eq": nil}
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

	var plans []Plan
	if err := cur.All(ctx, &plans); err != nil {
		return nil, err
	}

	// Sort by updated_at desc, limit in-memory
	sortPlans(plans)
	if opts.Limit > 0 && len(plans) > opts.Limit {
		plans = plans[:opts.Limit]
	}
	return plans, nil
}

// Count returns the number of plans matching the filter.
func (s *PlanStore) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.sd.CountDocuments(ctx, filter)
}

// Archive sets archived_at on a plan.
func (s *PlanStore) Archive(ctx context.Context, planID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"plan_id": planID}, bson.M{"$set": bson.M{"archived_at": now, "updated_at": now}})
}

// Unarchive clears archived_at on a plan.
func (s *PlanStore) Unarchive(ctx context.Context, planID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"plan_id": planID}, bson.M{"$set": bson.M{"archived_at": nil, "updated_at": now}})
}

// sortPlans sorts by updated_at descending.
func sortPlans(plans []Plan) {
	for i := 0; i < len(plans); i++ {
		for j := i + 1; j < len(plans); j++ {
			if plans[j].UpdatedAt.After(plans[i].UpdatedAt) {
				plans[i], plans[j] = plans[j], plans[i]
			}
		}
	}
}

// ------------------------------------------------------------------
// Collection passthrough
// ------------------------------------------------------------------

// Name returns the underlying collection name.
func (s *PlanStore) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *PlanStore) Database() *mongo.Database {
	return s.sd.Database()
}
