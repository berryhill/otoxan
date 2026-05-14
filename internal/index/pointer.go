package index

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MemoryPointer records the mapping between a MongoDB source document and its
// corresponding Qdrant vector point. It lives in the per-agent
// memory_pointers collection and enables re-index detection, deletion
// propagation, and audit.
type MemoryPointer struct {
	// PointerID is the unique identifier for this pointer document.
	PointerID string `bson:"pointer_id" json:"pointer_id"`

	// SourceID is the MongoDB _id of the source-of-truth document.
	SourceID string `bson:"source_id" json:"source_id"`

	// SourceType identifies the kind of document (e.g. "session", "plan",
	// "task", "build", "error").
	SourceType string `bson:"source_type" json:"source_type"`

	// SourceCollection is the MongoDB collection name where the source doc lives.
	SourceCollection string `bson:"source_collection" json:"source_collection"`

	// QdrantPointID is the UUID of the vector point in Qdrant.
	QdrantPointID string `bson:"qdrant_point_id" json:"qdrant_point_id"`

	// QdrantCollection is the Qdrant collection name (e.g. "agent_42_index").
	QdrantCollection string `bson:"qdrant_collection" json:"qdrant_collection"`

	// IndexedAt is when the document was last embedded and written to Qdrant.
	IndexedAt time.Time `bson:"indexed_at" json:"indexed_at"`

	// SourceUpdatedAt is the updated_at timestamp of the source doc at the
	// time of indexing. Used by FindStale to detect out-of-date pointers.
	SourceUpdatedAt time.Time `bson:"source_updated_at" json:"source_updated_at"`

	// Removed is true when the source doc has been soft-deleted and the
	// corresponding Qdrant point has been (or should be) removed.
	Removed bool `bson:"removed,omitempty" json:"removed,omitempty"`

	// RemovedAt records when the pointer was marked removed.
	RemovedAt *time.Time `bson:"removed_at,omitempty" json:"removed_at,omitempty"`
}

// PointerStore provides CRUD operations for memory_pointers documents.
type PointerStore struct {
	coll *mongo.Collection
}

// NewPointerStore creates a PointerStore backed by the given MongoDB collection.
// It ensures required indexes.
func NewPointerStore(coll *mongo.Collection) *PointerStore {
	ps := &PointerStore{coll: coll}
	_ = ps.ensureIndexes(context.Background())
	return ps
}

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (s *PointerStore) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "pointer_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "source_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "qdrant_point_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "source_type", Value: 1}}},
		{Keys: bson.D{{Key: "removed", Value: 1}}},
		{Keys: bson.D{{Key: "indexed_at", Value: 1}}},
	}
	_, err := s.coll.Indexes().CreateMany(ctx, indexes)
	return err
}

// ------------------------------------------------------------------
// Upsert
// ------------------------------------------------------------------

// Upsert inserts a new pointer or updates an existing one matched by pointer_id.
// It sets IndexedAt to now and clears Removed/RemovedAt.
func (s *PointerStore) Upsert(ctx context.Context, p *MemoryPointer) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	p.IndexedAt = now
	p.Removed = false
	p.RemovedAt = nil

	filter := bson.M{"pointer_id": p.PointerID}
	update := bson.M{"$set": p}
	opts := options.UpdateOne().SetUpsert(true)
	return s.coll.UpdateOne(ctx, filter, update, opts)
}

// ------------------------------------------------------------------
// FindStale
// ------------------------------------------------------------------

// FindStale returns pointers whose source document has been updated more
// recently than the pointer's SourceUpdatedAt. The caller provides a map of
// source_id -> current updated_at timestamps; any pointer whose SourceUpdatedAt
// is older is considered stale.
func (s *PointerStore) FindStale(ctx context.Context, sourceUpdatedAt map[string]time.Time) ([]MemoryPointer, error) {
	if len(sourceUpdatedAt) == 0 {
		return nil, nil
	}

	// Build an $or query of source_id + source_updated_at mismatch.
	var orConditions []bson.M
	for sourceID, updatedAt := range sourceUpdatedAt {
		orConditions = append(orConditions, bson.M{
			"source_id":         sourceID,
			"source_updated_at": bson.M{"$lt": updatedAt},
			"removed":           bson.M{"$ne": true},
		})
	}

	filter := bson.M{"$or": orConditions}
	cur, err := s.coll.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var out []MemoryPointer
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ------------------------------------------------------------------
// MarkRemoved
// ------------------------------------------------------------------

// MarkRemoved sets Removed=true and RemovedAt=now for the pointer matching
// the given pointer_id. It does not touch Qdrant; the caller is responsible
// for deleting the vector point.
func (s *PointerStore) MarkRemoved(ctx context.Context, pointerID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	filter := bson.M{"pointer_id": pointerID}
	update := bson.M{"$set": bson.M{
		"removed":    true,
		"removed_at": now,
	}}
	return s.coll.UpdateOne(ctx, filter, update)
}

// ------------------------------------------------------------------
// FindBySource
// ------------------------------------------------------------------

// FindBySource returns the pointer for a given source_id, or mongo.ErrNoDocuments
// if none exists.
func (s *PointerStore) FindBySource(ctx context.Context, sourceID string) (*MemoryPointer, error) {
	filter := bson.M{"source_id": sourceID, "removed": bson.M{"$ne": true}}
	sr := s.coll.FindOne(ctx, filter)
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var p MemoryPointer
	if err := sr.Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

// Collection returns the underlying MongoDB collection.
func (s *PointerStore) Collection() *mongo.Collection {
	return s.coll
}
