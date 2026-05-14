// Package agentregistry provides a MongoDB-backed store for the global agent
// registry. Every agent registered here gets a document in otoxan_global.agents
// and an isolated per-agent database (otoxan_agent_<name>).
package agentregistry

import (
	"context"
	"fmt"
	"time"

	"github.com/silas/otoxan/internal/softdelete"
	"github.com/silas/otoxan/internal/state"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// AgentRegistryStore
// ------------------------------------------------------------------

// NewStore creates an AgentRegistryStore backed by the global agents collection.
// It ensures required indexes on the agents collection.
func NewStore(client *mongo.Client) (*Store, error) {
	globalDB := state.GlobalDB(client)
	coll := globalDB.Collection("agents")
	sd := softdelete.NewSoftDelete(coll)
	s := &Store{client: client, sd: sd}
	if err := s.ensureIndexes(context.Background()); err != nil {
		return nil, fmt.Errorf("ensure indexes: %w", err)
	}
	return s, nil
}

// Store is a MongoDB-backed store for agent registry documents with soft-delete
// semantics.
type Store struct {
	client *mongo.Client
	sd     *softdelete.SoftDeleteCollection
}

// ensureIndexes creates required indexes for the agents collection.
func (s *Store) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "name", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "created_at", Value: 1}}},
	}
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// ------------------------------------------------------------------
// CRUD
// ------------------------------------------------------------------

// Register creates a new agent registry document in the global DB and ensures
// the per-agent database exists. The agent name must pass ValidateAgentName.
// If the agent already exists and is not soft-deleted, it returns an error.
func (s *Store) Register(ctx context.Context, name, role string) (*mongo.InsertOneResult, error) {
	if err := state.ValidateAgentName(name); err != nil {
		return nil, err
	}

	// Ensure the per-agent DB exists by creating a sentinel collection.
	agentDB, err := state.AgentDB(s.client, name)
	if err != nil {
		return nil, err
	}
	if err := agentDB.CreateCollection(ctx, "__init"); err != nil {
		return nil, fmt.Errorf("create agent init collection: %w", err)
	}

	now := time.Now().UTC()
	agent := &AgentRegistryDoc{
		Name:      name,
		Role:      role,
		DBName:    agentDB.Name(),
		Status:    AgentStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}

	return s.sd.InsertOne(ctx, agent)
}

// Get retrieves a single agent by name. Returns mongo.ErrNoDocuments if
// not found (or soft-deleted).
func (s *Store) Get(ctx context.Context, name string) (*AgentRegistryDoc, error) {
	sr := s.sd.FindOne(ctx, bson.M{"name": name})
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var a AgentRegistryDoc
	if err := sr.Decode(&a); err != nil {
		return nil, err
	}
	return &a, nil
}

// GetWithDeleted retrieves an agent including soft-deleted ones.
func (s *Store) GetWithDeleted(ctx context.Context, name string) (*AgentRegistryDoc, error) {
	sr := s.sd.FindOne(ctx, bson.M{"name": name}, softdelete.WithIncludeDeleted())
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var a AgentRegistryDoc
	if err := sr.Decode(&a); err != nil {
		return nil, err
	}
	return &a, nil
}

// Update patches fields of an existing agent. Sets updated_at automatically.
func (s *Store) Update(ctx context.Context, name string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"name": name}, bson.M{"$set": updates})
}

// Delete soft-deletes an agent by name.
func (s *Store) Delete(ctx context.Context, name string) (*mongo.UpdateResult, error) {
	return s.sd.Delete(ctx, bson.M{"name": name})
}

// Restore un-deletes a soft-deleted agent.
func (s *Store) Restore(ctx context.Context, name string) (*mongo.UpdateResult, error) {
	return s.sd.Restore(ctx, bson.M{"name": name})
}

// HardDelete permanently removes an agent.
func (s *Store) HardDelete(ctx context.Context, name string) (*mongo.DeleteResult, error) {
	return s.sd.HardDelete(ctx, bson.M{"name": name})
}

// ------------------------------------------------------------------
// List
// ------------------------------------------------------------------

// ListOptions configures the List query.
type ListOptions struct {
	Status         []AgentStatus
	Limit          int
	IncludeDeleted bool
}

// List returns agents matching the provided filters, sorted by created_at descending.
func (s *Store) List(ctx context.Context, opts ListOptions) ([]AgentRegistryDoc, error) {
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

	var agents []AgentRegistryDoc
	if err := cur.All(ctx, &agents); err != nil {
		return nil, err
	}

	// Sort by created_at desc, limit in-memory
	sortAgents(agents)
	if opts.Limit > 0 && len(agents) > opts.Limit {
		agents = agents[:opts.Limit]
	}
	return agents, nil
}

// Count returns the number of agents matching the filter.
func (s *Store) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.sd.CountDocuments(ctx, filter)
}

// sortAgents sorts by created_at descending.
func sortAgents(agents []AgentRegistryDoc) {
	for i := 0; i < len(agents); i++ {
		for j := i + 1; j < len(agents); j++ {
			if agents[j].CreatedAt.After(agents[i].CreatedAt) {
				agents[i], agents[j] = agents[j], agents[i]
			}
		}
	}
}

// Name returns the underlying collection name.
func (s *Store) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *Store) Database() *mongo.Database {
	return s.sd.Database()
}
