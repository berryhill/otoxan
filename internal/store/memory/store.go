// Package memory provides a MongoDB-backed store for agent memory documents
// with soft-delete semantics. Vector storage is delegated to Qdrant.
package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/silas/otoxan/internal/qdrant"
	"github.com/silas/otoxan/internal/softdelete"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// NewMemoryStore creates a MemoryStore backed by the given MongoDB collection
// and Qdrant client. It ensures required indexes on the memory collection.
func NewMemoryStore(coll *mongo.Collection, qdrantClient *qdrant.Client) *MemoryStore {
	sd := softdelete.NewSoftDelete(coll)
	ms := &MemoryStore{sd: sd, qdrant: qdrantClient}
	_ = ms.ensureIndexes(context.Background())
	return ms
}

// MemoryStore is a MongoDB-backed store for memory documents with soft-delete
// semantics. Vector data lives in Qdrant; metadata lives in MongoDB.
type MemoryStore struct {
	sd     *softdelete.SoftDeleteCollection
	qdrant *qdrant.Client
}

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (s *MemoryStore) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "memory_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "agent_id", Value: 1}}},
		{Keys: bson.D{{Key: "session_id", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "type", Value: 1}}},
		{Keys: bson.D{{Key: "created_at", Value: 1}}},
		{Keys: bson.D{{Key: "vector_id", Value: 1}}, Options: options.Index().SetSparse(true)},
	}
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// ------------------------------------------------------------------
// CRUD
// ------------------------------------------------------------------

// Create inserts a new memory document. The caller must set MemoryID and AgentID.
// Defaults are applied for optional fields.
func (s *MemoryStore) Create(ctx context.Context, mem *Memory) (*mongo.InsertOneResult, error) {
	now := time.Now().UTC()
	if mem.CreatedAt.IsZero() {
		mem.CreatedAt = now
	}
	if mem.UpdatedAt.IsZero() {
		mem.UpdatedAt = now
	}
	if mem.Type == "" {
		mem.Type = TypeObservation
	}
	if mem.Tags == nil {
		mem.Tags = []string{}
	}

	// If a vector is provided and Qdrant is configured, upsert it.
	if s.qdrant != nil && mem.Vector != nil && mem.VectorID == "" {
		vectorID := fmt.Sprintf("mem_%s", mem.MemoryID)
		mem.VectorID = vectorID
		point := qdrant.Point{
			ID:      vectorID,
			Vector:  mem.Vector,
			Payload: mem.QdrantPayload(),
		}
		if err := s.qdrant.Upsert(ctx, s.qdrantCollection(), []qdrant.Point{point}); err != nil {
			return nil, fmt.Errorf("qdrant upsert: %w", err)
		}
	}

	return s.sd.InsertOne(ctx, mem)
}

// Get retrieves a single memory by memory_id. Returns mongo.ErrNoDocuments if
// not found (or soft-deleted).
func (s *MemoryStore) Get(ctx context.Context, memoryID string) (*Memory, error) {
	sr := s.sd.FindOne(ctx, bson.M{"memory_id": memoryID})
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var m Memory
	if err := sr.Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// GetWithDeleted retrieves a memory including soft-deleted ones.
func (s *MemoryStore) GetWithDeleted(ctx context.Context, memoryID string) (*Memory, error) {
	sr := s.sd.FindOne(ctx, bson.M{"memory_id": memoryID}, softdelete.WithIncludeDeleted())
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var m Memory
	if err := sr.Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Update patches fields of an existing memory. Sets updated_at automatically.
func (s *MemoryStore) Update(ctx context.Context, memoryID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"memory_id": memoryID}, bson.M{"$set": updates})
}

// Delete soft-deletes a memory by memory_id. Also removes the vector from Qdrant.
func (s *MemoryStore) Delete(ctx context.Context, memoryID string) (*mongo.UpdateResult, error) {
	// Best-effort Qdrant cleanup
	if s.qdrant != nil {
		_ = s.qdrant.DeletePoints(ctx, s.qdrantCollection(), []string{fmt.Sprintf("mem_%s", memoryID)})
	}
	return s.sd.Delete(ctx, bson.M{"memory_id": memoryID})
}

// Restore un-deletes a soft-deleted memory.
func (s *MemoryStore) Restore(ctx context.Context, memoryID string) (*mongo.UpdateResult, error) {
	return s.sd.Restore(ctx, bson.M{"memory_id": memoryID})
}

// HardDelete permanently removes a memory. Also removes the vector from Qdrant.
func (s *MemoryStore) HardDelete(ctx context.Context, memoryID string) (*mongo.DeleteResult, error) {
	if s.qdrant != nil {
		_ = s.qdrant.DeletePoints(ctx, s.qdrantCollection(), []string{fmt.Sprintf("mem_%s", memoryID)})
	}
	return s.sd.HardDelete(ctx, bson.M{"memory_id": memoryID})
}

// ------------------------------------------------------------------
// Vector search
// ------------------------------------------------------------------

// MemoryHit is a single result from a vector search.
type MemoryHit struct {
	MemoryID string  `json:"memory_id"`
	Score    float32 `json:"score"`
	Payload  map[string]interface{} `json:"payload,omitempty"`
}

// Search performs a nearest-neighbour search via Qdrant and returns metadata
// hits. If Qdrant is not configured, returns an empty slice and no error.
func (s *MemoryStore) Search(ctx context.Context, query []float32, k int) ([]*MemoryHit, error) {
	if s.qdrant == nil {
		return nil, nil
	}
	results, err := s.qdrant.Search(ctx, s.qdrantCollection(), query, k)
	if err != nil {
		return nil, fmt.Errorf("qdrant search: %w", err)
	}
	hits := make([]*MemoryHit, 0, len(results))
	for _, r := range results {
		hits = append(hits, &MemoryHit{
			MemoryID: extractMemoryID(r.ID),
			Score:    r.Score,
			Payload:  r.Payload,
		})
	}
	return hits, nil
}

// ------------------------------------------------------------------
// List / Query
// ------------------------------------------------------------------

// ListOptions configures the List query.
type ListOptions struct {
	AgentID        string
	Type           []MemoryType
	Limit          int
	IncludeDeleted bool
}

// List returns memories matching the provided filters, sorted by created_at descending.
func (s *MemoryStore) List(ctx context.Context, opts ListOptions) ([]Memory, error) {
	filter := bson.M{}
	if opts.AgentID != "" {
		filter["agent_id"] = opts.AgentID
	}
	if len(opts.Type) > 0 {
		if len(opts.Type) == 1 {
			filter["type"] = opts.Type[0]
		} else {
			types := make([]string, len(opts.Type))
			for i, t := range opts.Type {
				types[i] = string(t)
			}
			filter["type"] = bson.M{"$in": types}
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

	var memories []Memory
	if err := cur.All(ctx, &memories); err != nil {
		return nil, err
	}

	// Sort by created_at desc, limit in-memory
	sortMemories(memories)
	if opts.Limit > 0 && len(memories) > opts.Limit {
		memories = memories[:opts.Limit]
	}
	return memories, nil
}

// Count returns the number of memories matching the filter.
func (s *MemoryStore) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.sd.CountDocuments(ctx, filter)
}

// sortMemories sorts by created_at descending.
func sortMemories(memories []Memory) {
	for i := 0; i < len(memories); i++ {
		for j := i + 1; j < len(memories); j++ {
			if memories[j].CreatedAt.After(memories[i].CreatedAt) {
				memories[i], memories[j] = memories[j], memories[i]
			}
		}
	}
}

// ------------------------------------------------------------------
// Collection passthrough
// ------------------------------------------------------------------

// Name returns the underlying collection name.
func (s *MemoryStore) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *MemoryStore) Database() *mongo.Database {
	return s.sd.Database()
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

func (s *MemoryStore) qdrantCollection() string {
	return s.sd.Name()
}

func extractMemoryID(vectorID string) string {
	// vector IDs are "mem_<memory_id>"
	if len(vectorID) > 4 && vectorID[:4] == "mem_" {
		return vectorID[4:]
	}
	return vectorID
}
