// Package identitystore provides a MongoDB-backed store for identity manifest
// documents. Identities are versioned personas that can be activated and
// retired, with exactly one identity per name being active at any time.
package identitystore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/silas/otoxan/internal/softdelete"
	"github.com/silas/otoxan/internal/state"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ErrIdentityNotFound is returned when an identity document is not found.
var ErrIdentityNotFound = errors.New("identity not found")

// ErrIdentityExists is returned when creating a duplicate identity+version.
var ErrIdentityExists = errors.New("identity version already exists")

// ErrNoActiveIdentity is returned when no active identity exists for a name.
var ErrNoActiveIdentity = errors.New("no active identity found")

// ErrIdentityNotInactive is returned when activating an identity that is not inactive.
var ErrIdentityNotInactive = errors.New("identity must be inactive to activate")

// NewStore creates an IdentityStore backed by the global identities collection
// in otoxan_global. It ensures required indexes.
func NewStore(client *mongo.Client) (*Store, error) {
	globalDB := state.GlobalDB(client)
	coll := globalDB.Collection("identities")
	sd := softdelete.NewSoftDelete(coll)
	s := &Store{sd: sd}
	if err := s.ensureIndexes(context.Background()); err != nil {
		return nil, fmt.Errorf("ensure indexes: %w", err)
	}
	return s, nil
}

// Store is a MongoDB-backed store for identity manifest documents with soft-delete
// semantics and atomic activation via unique partial index.
type Store struct {
	sd *softdelete.SoftDeleteCollection
}

// ensureIndexes creates required indexes for the identities collection.
func (s *Store) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		// Unique composite: name + version (each version of an identity is unique)
		{Keys: bson.D{{Key: "name", Value: 1}, {Key: "version", Value: 1}}, Options: options.Index().SetUnique(true)},
		// Unique: only one active identity per name
		{Keys: bson.D{{Key: "name", Value: 1}, {Key: "status", Value: 1}}, Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.D{{Key: "status", Value: "active"}})},
		// Query indexes
		{Keys: bson.D{{Key: "name", Value: 1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "created_at", Value: 1}}},
	}
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// ------------------------------------------------------------------
// Create
// ------------------------------------------------------------------

// Create inserts a new identity manifest document. The name+version must be unique.
// If status is not set, it defaults to StatusInactive.
func (s *Store) Create(ctx context.Context, identity *IdentityManifest) (*mongo.InsertOneResult, error) {
	now := time.Now().UTC()
	if identity.CreatedAt.IsZero() {
		identity.CreatedAt = now
	}
	if identity.UpdatedAt.IsZero() {
		identity.UpdatedAt = now
	}
	if identity.Status == "" {
		identity.Status = StatusInactive
	}
	if identity.Tags == nil {
		identity.Tags = []string{}
	}
	if identity.ProviderProfiles == nil {
		identity.ProviderProfiles = make(map[ProviderType]string)
	}

	// Check for duplicate name+version
	existing, err := s.sd.CountDocuments(ctx, bson.M{
		"name":    identity.Name,
		"version": identity.Version,
	})
	if err != nil {
		return nil, err
	}
	if existing > 0 {
		return nil, ErrIdentityExists
	}

	return s.sd.InsertOne(ctx, identity)
}

// ------------------------------------------------------------------
// Get
// ------------------------------------------------------------------

// Get retrieves a single identity by name and version.
func (s *Store) Get(ctx context.Context, name, version string) (*IdentityManifest, error) {
	sr := s.sd.FindOne(ctx, bson.M{"name": name, "version": version})
	if sr.Err() != nil {
		if errors.Is(sr.Err(), mongo.ErrNoDocuments) {
			return nil, ErrIdentityNotFound
		}
		return nil, sr.Err()
	}
	var identity IdentityManifest
	if err := sr.Decode(&identity); err != nil {
		return nil, err
	}
	return &identity, nil
}

// GetActive retrieves the currently active identity for a given name.
// Returns ErrNoActiveIdentity if no active identity exists.
func (s *Store) GetActive(ctx context.Context, name string) (*IdentityManifest, error) {
	sr := s.sd.FindOne(ctx, bson.M{"name": name, "status": string(StatusActive)})
	if sr.Err() != nil {
		if errors.Is(sr.Err(), mongo.ErrNoDocuments) {
			return nil, ErrNoActiveIdentity
		}
		return nil, sr.Err()
	}
	var identity IdentityManifest
	if err := sr.Decode(&identity); err != nil {
		return nil, err
	}
	return &identity, nil
}

// GetWithDeleted retrieves an identity including soft-deleted ones.
func (s *Store) GetWithDeleted(ctx context.Context, name, version string) (*IdentityManifest, error) {
	sr := s.sd.FindOne(ctx, bson.M{"name": name, "version": version}, softdelete.WithIncludeDeleted())
	if sr.Err() != nil {
		if errors.Is(sr.Err(), mongo.ErrNoDocuments) {
			return nil, ErrIdentityNotFound
		}
		return nil, sr.Err()
	}
	var identity IdentityManifest
	if err := sr.Decode(&identity); err != nil {
		return nil, err
	}
	return &identity, nil
}

// ------------------------------------------------------------------
// Update
// ------------------------------------------------------------------

// Update patches fields of an existing identity. Sets updated_at automatically.
func (s *Store) Update(ctx context.Context, name, version string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"name": name, "version": version}, bson.M{"$set": updates})
}

// ------------------------------------------------------------------
// Activate (transactional flip)
// ------------------------------------------------------------------

// Activate makes a specific identity version the active one. This operation:
// 1. Deactivates any currently active identity for this name
// 2. Activates the target identity
//
// The activation is atomic: a unique partial index on (name, status=active)
// ensures only one identity per name can be active. If a concurrent activation
// races, the second update will fail with a duplicate key error.
func (s *Store) Activate(ctx context.Context, name, version string) error {
	now := time.Now().UTC()

	// Verify target identity exists and is inactive
	target, err := s.Get(ctx, name, version)
	if err != nil {
		return err
	}
	if target.Status != StatusInactive {
		return ErrIdentityNotInactive
	}

	// Step 1: Deactivate any currently active identity for this name
	// Clear activated_at when deactivating
	_, err = s.sd.UpdateOne(ctx,
		bson.M{"name": name, "status": string(StatusActive)},
		bson.M{"$set": bson.M{
			"status":        string(StatusInactive),
			"updated_at":    now,
			"activated_at":  nil,
		}},
	)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return fmt.Errorf("deactivate current: %w", err)
	}

	// Step 2: Activate the target identity
	// The unique partial index ensures only one active identity per name
	res, err := s.sd.UpdateOne(ctx,
		bson.M{"name": name, "version": version},
		bson.M{"$set": bson.M{
			"status":       string(StatusActive),
			"updated_at":   now,
			"activated_at":  now,
		}},
	)
	if err != nil {
		return fmt.Errorf("activate target: %w", err)
	}
	if res.MatchedCount == 0 {
		return ErrIdentityNotFound
	}

	return nil
}

// ------------------------------------------------------------------
// Retire
// ------------------------------------------------------------------

// Retire marks an identity version as retired. A retired identity cannot be
// activated again. The identity must be inactive (not active) to be retired.
func (s *Store) Retire(ctx context.Context, name, version string) error {
	now := time.Now().UTC()

	// Verify identity exists
	identity, err := s.Get(ctx, name, version)
	if err != nil {
		return err
	}

	// Cannot retire an active identity
	if identity.Status == StatusActive {
		return errors.New("cannot retire an active identity; deactivate it first")
	}

	// Cannot retire an already-retired identity
	if identity.Status == StatusRetired {
		return errors.New("identity is already retired")
	}

	_, err = s.sd.UpdateOne(ctx,
		bson.M{"name": name, "version": version},
		bson.M{"$set": bson.M{
			"status":     string(StatusRetired),
			"updated_at": now,
			"retired_at": now,
		}},
	)
	return err
}

// ------------------------------------------------------------------
// Delete (soft)
// ------------------------------------------------------------------

// Delete soft-deletes an identity by name and version.
func (s *Store) Delete(ctx context.Context, name, version string) (*mongo.UpdateResult, error) {
	return s.sd.Delete(ctx, bson.M{"name": name, "version": version})
}

// Restore un-deletes a soft-deleted identity.
func (s *Store) Restore(ctx context.Context, name, version string) (*mongo.UpdateResult, error) {
	return s.sd.Restore(ctx, bson.M{"name": name, "version": version})
}

// HardDelete permanently removes an identity.
func (s *Store) HardDelete(ctx context.Context, name, version string) (*mongo.DeleteResult, error) {
	return s.sd.HardDelete(ctx, bson.M{"name": name, "version": version})
}

// ------------------------------------------------------------------
// List
// ------------------------------------------------------------------

// ListOptions configures the List query.
type ListOptions struct {
	Name          string
	Status        IdentityStatus
	Tags          []string
	Limit         int
	Offset        int
	IncludeDeleted bool
}

// List returns identities matching the provided filters.
func (s *Store) List(ctx context.Context, opts ListOptions) ([]IdentityManifest, error) {
	filter := bson.M{}
	if opts.Name != "" {
		filter["name"] = opts.Name
	}
	if opts.Status != "" {
		filter["status"] = string(opts.Status)
	}
	if len(opts.Tags) > 0 {
		filter["tags"] = bson.M{"$all": opts.Tags}
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

	var identities []IdentityManifest
	if err := cur.All(ctx, &identities); err != nil {
		return nil, err
	}

	// Sort by created_at desc
	sortIdentities(identities)

	// Apply offset and limit
	if opts.Offset > 0 && opts.Offset < len(identities) {
		identities = identities[opts.Offset:]
	}
	if opts.Limit > 0 && opts.Limit < len(identities) {
		identities = identities[:opts.Limit]
	}

	return identities, nil
}

// ListVersions returns all versions of an identity, sorted by created_at descending.
func (s *Store) ListVersions(ctx context.Context, name string, includeDeleted bool) ([]IdentityManifest, error) {
	opts := ListOptions{Name: name, IncludeDeleted: includeDeleted}
	return s.List(ctx, opts)
}

// Count returns the number of identities matching the filter.
func (s *Store) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.sd.CountDocuments(ctx, filter)
}

// Name returns the underlying collection name.
func (s *Store) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *Store) Database() *mongo.Database {
	return s.sd.Database()
}

// sortIdentities sorts by created_at descending.
func sortIdentities(identities []IdentityManifest) {
	for i := 0; i < len(identities); i++ {
		for j := i + 1; j < len(identities); j++ {
			if identities[j].CreatedAt.After(identities[i].CreatedAt) {
				identities[i], identities[j] = identities[j], identities[i]
			}
		}
	}
}
