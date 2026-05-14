// Package scopestore provides a MongoDB-backed store for Infisical secret scopes.
//
// It tracks which otoxan agent is allowed to ask for which subset of secrets.
// This is otoxan-side authorization on top of Infisical-side authentication.
// Putting it in Mongo lets Xander deny scopes with one update, no Infisical
// round-trip.
package scopestore

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
// Document model
// ------------------------------------------------------------------

// ScopeDoc is the canonical BSON shape for a scope document in the
// otoxan_global.infisical_scopes collection.
type ScopeDoc struct {
	AgentName   string    `bson:"agent_name" json:"agent_name"`
	SecretPaths []string  `bson:"secret_paths" json:"secret_paths"`
	CreatedAt   time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time `bson:"updated_at" json:"updated_at"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// ------------------------------------------------------------------
// Store
// ------------------------------------------------------------------

// Store is a MongoDB-backed store for infisical scope documents with soft-delete
// semantics.
type Store struct {
	client *mongo.Client
	sd     *softdelete.SoftDeleteCollection
}

// NewStore creates a ScopeStore backed by the global infisical_scopes collection.
// It ensures required indexes on the collection.
func NewStore(client *mongo.Client) (*Store, error) {
	globalDB := state.GlobalDB(client)
	coll := globalDB.Collection("infisical_scopes")
	sd := softdelete.NewSoftDelete(coll)
	s := &Store{client: client, sd: sd}
	if err := s.ensureIndexes(context.Background()); err != nil {
		return nil, fmt.Errorf("ensure indexes: %w", err)
	}
	return s, nil
}

// ensureIndexes creates required indexes for the infisical_scopes collection.
func (s *Store) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "agent_name", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "created_at", Value: 1}}},
	}
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// ------------------------------------------------------------------
// Grant / Revoke / List
// ------------------------------------------------------------------

// Grant creates or updates a scope document for an agent, giving it access to
// the specified secret paths. Previous paths for that agent are replaced.
func (s *Store) Grant(ctx context.Context, agentName string, secretPaths []string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	filter := bson.M{"agent_name": agentName}
	update := bson.M{
		"$set": bson.M{
			"agent_name":   agentName,
			"secret_paths": secretPaths,
			"updated_at":   now,
		},
		"$setOnInsert": bson.M{
			"created_at": now,
		},
	}
	// Upsert so we can both create and update in one call.
	opts := options.UpdateOne().SetUpsert(true)
	return s.sd.Database().Collection(s.sd.Name()).UpdateOne(ctx, filter, update, opts)
}

// Revoke removes all scope entries for an agent by soft-deleting its document.
func (s *Store) Revoke(ctx context.Context, agentName string) (*mongo.UpdateResult, error) {
	return s.sd.Delete(ctx, bson.M{"agent_name": agentName})
}

// ListOptions configures the List query.
type ListOptions struct {
	AgentName      string
	Limit          int
	IncludeDeleted bool
}

// List returns scope documents matching the provided filters, sorted by
// created_at descending.
func (s *Store) List(ctx context.Context, opts ListOptions) ([]ScopeDoc, error) {
	filter := bson.M{}
	if opts.AgentName != "" {
		filter["agent_name"] = opts.AgentName
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

	var scopes []ScopeDoc
	if err := cur.All(ctx, &scopes); err != nil {
		return nil, err
	}

	// Sort by created_at desc, limit in-memory
	sortScopes(scopes)
	if opts.Limit > 0 && len(scopes) > opts.Limit {
		scopes = scopes[:opts.Limit]
	}
	return scopes, nil
}

// sortScopes sorts by created_at descending, using agent_name as a stable
// tie-breaker so tests are deterministic when grants happen within the
// same clock tick.
func sortScopes(scopes []ScopeDoc) {
	for i := 0; i < len(scopes); i++ {
		for j := i + 1; j < len(scopes); j++ {
			swap := false
			if scopes[j].CreatedAt.After(scopes[i].CreatedAt) {
				swap = true
			} else if scopes[j].CreatedAt.Equal(scopes[i].CreatedAt) && scopes[j].AgentName < scopes[i].AgentName {
				swap = true
			}
			if swap {
				scopes[i], scopes[j] = scopes[j], scopes[i]
			}
		}
	}
}

// Get retrieves a single agent's scope by name. Returns mongo.ErrNoDocuments if
// not found (or soft-deleted).
func (s *Store) Get(ctx context.Context, agentName string) (*ScopeDoc, error) {
	sr := s.sd.FindOne(ctx, bson.M{"agent_name": agentName})
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var sc ScopeDoc
	if err := sr.Decode(&sc); err != nil {
		return nil, err
	}
	return &sc, nil
}

// ------------------------------------------------------------------
// Passthrough / metadata
// ------------------------------------------------------------------

// Name returns the underlying collection name.
func (s *Store) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *Store) Database() *mongo.Database {
	return s.sd.Database()
}
