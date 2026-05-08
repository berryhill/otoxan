// Package flows provides a MongoDB-backed store for flow documents with
// soft-delete semantics.
package flows

import (
	"context"
	"time"

	"github.com/silas/otoxan/internal/softdelete"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// NewFlowStore creates a FlowStore backed by the given MongoDB collection.
// It ensures required indexes on the flows collection.
func NewFlowStore(coll *mongo.Collection) *FlowStore {
	sd := softdelete.NewSoftDelete(coll)
	fs := &FlowStore{sd: sd}
	_ = fs.ensureIndexes(context.Background())
	return fs
}

// FlowStore is a MongoDB-backed store for flow documents with soft-delete
// semantics.
type FlowStore struct {
	sd *softdelete.SoftDeleteCollection
}

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (s *FlowStore) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "flow_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "updated_at", Value: 1}}},
		{Keys: bson.D{{Key: "initiative_id", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "team_id", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "session_id", Value: 1}}, Options: options.Index().SetSparse(true)},
	}
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// ------------------------------------------------------------------
// CRUD
// ------------------------------------------------------------------

// Create inserts a new flow document. The caller must set FlowID and Name.
// Defaults are applied for optional fields.
func (s *FlowStore) Create(ctx context.Context, flow *Flow) (*mongo.InsertOneResult, error) {
	now := time.Now().UTC()
	if flow.CreatedAt.IsZero() {
		flow.CreatedAt = now
	}
	if flow.UpdatedAt.IsZero() {
		flow.UpdatedAt = now
	}
	if flow.Status == "" {
		flow.Status = StatusDraft
	}
	if flow.Steps == nil {
		flow.Steps = []FlowStep{}
	}
	return s.sd.InsertOne(ctx, flow)
}

// Get retrieves a single flow by flow_id. Returns mongo.ErrNoDocuments if
// not found (or soft-deleted).
func (s *FlowStore) Get(ctx context.Context, flowID string) (*Flow, error) {
	sr := s.sd.FindOne(ctx, bson.M{"flow_id": flowID})
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var f Flow
	if err := sr.Decode(&f); err != nil {
		return nil, err
	}
	return &f, nil
}

// GetWithDeleted retrieves a flow including soft-deleted ones.
func (s *FlowStore) GetWithDeleted(ctx context.Context, flowID string) (*Flow, error) {
	sr := s.sd.FindOne(ctx, bson.M{"flow_id": flowID}, softdelete.WithIncludeDeleted())
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var f Flow
	if err := sr.Decode(&f); err != nil {
		return nil, err
	}
	return &f, nil
}

// Update patches fields of an existing flow. Sets updated_at automatically.
func (s *FlowStore) Update(ctx context.Context, flowID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"flow_id": flowID}, bson.M{"$set": updates})
}

// Delete soft-deletes a flow by flow_id.
func (s *FlowStore) Delete(ctx context.Context, flowID string) (*mongo.UpdateResult, error) {
	return s.sd.Delete(ctx, bson.M{"flow_id": flowID})
}

// Restore un-deletes a soft-deleted flow.
func (s *FlowStore) Restore(ctx context.Context, flowID string) (*mongo.UpdateResult, error) {
	return s.sd.Restore(ctx, bson.M{"flow_id": flowID})
}

// HardDelete permanently removes a flow.
func (s *FlowStore) HardDelete(ctx context.Context, flowID string) (*mongo.DeleteResult, error) {
	return s.sd.HardDelete(ctx, bson.M{"flow_id": flowID})
}

// ------------------------------------------------------------------
// List / Query
// ------------------------------------------------------------------

// ListOptions configures the List query.
type ListOptions struct {
	Status         []FlowStatus
	Limit          int
	IncludeDeleted bool
}

// List returns flows matching the provided filters, sorted by updated_at descending.
func (s *FlowStore) List(ctx context.Context, opts ListOptions) ([]Flow, error) {
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

	var flows []Flow
	if err := cur.All(ctx, &flows); err != nil {
		return nil, err
	}

	// Sort by updated_at desc, limit in-memory
	sortFlows(flows)
	if opts.Limit > 0 && len(flows) > opts.Limit {
		flows = flows[:opts.Limit]
	}
	return flows, nil
}

// Count returns the number of flows matching the filter.
func (s *FlowStore) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.sd.CountDocuments(ctx, filter)
}

// sortFlows sorts by updated_at descending.
func sortFlows(flows []Flow) {
	for i := 0; i < len(flows); i++ {
		for j := i + 1; j < len(flows); j++ {
			if flows[j].UpdatedAt.After(flows[i].UpdatedAt) {
				flows[i], flows[j] = flows[j], flows[i]
			}
		}
	}
}

// ------------------------------------------------------------------
// Collection passthrough
// ------------------------------------------------------------------

// Name returns the underlying collection name.
func (s *FlowStore) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *FlowStore) Database() *mongo.Database {
	return s.sd.Database()
}
