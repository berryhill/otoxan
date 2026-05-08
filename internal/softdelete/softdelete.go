// Package softdelete provides a transparent wrapper around a MongoDB collection
// that implements soft-delete semantics. All queries automatically filter out
// documents where deleted=true unless an IncludeDeleted option is set.
package softdelete

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// SoftDeleteCollection is a transparent wrapper around a mongo.Collection
// that implements soft deletes.
type SoftDeleteCollection struct {
	coll *mongo.Collection
}

// NewSoftDelete creates a new SoftDeleteCollection wrapping the given collection.
func NewSoftDelete(coll *mongo.Collection) *SoftDeleteCollection {
	return &SoftDeleteCollection{coll: coll}
}

// ------------------------------------------------------------------
// Internal helpers
// ------------------------------------------------------------------

const (
	softDeleteField   = "deleted"
	softDeleteAtField = "deleted_at"
)

// addDeletedFilter returns a new filter that excludes soft-deleted documents
// unless includeDeleted is true. If the filter already contains a "deleted"
// key, it is not modified.
func addDeletedFilter(filter bson.M, includeDeleted bool) bson.M {
	if includeDeleted {
		return filter
	}
	if filter == nil {
		filter = bson.M{}
	}
	// Do not double-add if the caller already specified a deleted predicate.
	if _, ok := filter[softDeleteField]; ok {
		return filter
	}
	filter[softDeleteField] = bson.M{"$ne": true}
	return filter
}

// ------------------------------------------------------------------
// Read operations (auto-filter deleted)
// ------------------------------------------------------------------

// Find returns a cursor over documents matching filter, excluding soft-deleted
// documents unless opts includes IncludeDeleted.
func (s *SoftDeleteCollection) Find(ctx context.Context, filter bson.M, opts ...Option) (*mongo.Cursor, error) {
	cfg := applyOpts(opts)
	filter = addDeletedFilter(filter, cfg.includeDeleted)
	return s.coll.Find(ctx, filter)
}

// FindOne returns a single document matching filter.
func (s *SoftDeleteCollection) FindOne(ctx context.Context, filter bson.M, opts ...Option) *mongo.SingleResult {
	cfg := applyOpts(opts)
	filter = addDeletedFilter(filter, cfg.includeDeleted)
	return s.coll.FindOne(ctx, filter)
}

// CountDocuments returns the number of documents matching filter.
func (s *SoftDeleteCollection) CountDocuments(ctx context.Context, filter bson.M, opts ...Option) (int64, error) {
	cfg := applyOpts(opts)
	filter = addDeletedFilter(filter, cfg.includeDeleted)
	return s.coll.CountDocuments(ctx, filter)
}

// Distinct returns the distinct values for a field.
func (s *SoftDeleteCollection) Distinct(ctx context.Context, fieldName string, filter bson.M, opts ...Option) *mongo.DistinctResult {
	cfg := applyOpts(opts)
	filter = addDeletedFilter(filter, cfg.includeDeleted)
	return s.coll.Distinct(ctx, fieldName, filter)
}

// ------------------------------------------------------------------
// Write operations
// ------------------------------------------------------------------

// InsertOne inserts a single document, stripping any deleted/deleted_at fields
// so new documents are never pre-marked deleted.
func (s *SoftDeleteCollection) InsertOne(ctx context.Context, document interface{}, opts ...options.Lister[options.InsertOneOptions]) (*mongo.InsertOneResult, error) {
	doc, err := stripDeleteFields(document)
	if err != nil {
		return nil, err
	}
	return s.coll.InsertOne(ctx, doc, opts...)
}

// InsertMany inserts multiple documents, stripping deleted fields.
func (s *SoftDeleteCollection) InsertMany(ctx context.Context, documents []interface{}, opts ...options.Lister[options.InsertManyOptions]) (*mongo.InsertManyResult, error) {
	docs := make([]interface{}, len(documents))
	for i, d := range documents {
		stripped, err := stripDeleteFields(d)
		if err != nil {
			return nil, err
		}
		docs[i] = stripped
	}
	return s.coll.InsertMany(ctx, docs, opts...)
}

// UpdateOne updates a single document matching filter.
func (s *SoftDeleteCollection) UpdateOne(ctx context.Context, filter bson.M, update interface{}, opts ...Option) (*mongo.UpdateResult, error) {
	cfg := applyOpts(opts)
	filter = addDeletedFilter(filter, cfg.includeDeleted)
	return s.coll.UpdateOne(ctx, filter, update)
}

// UpdateMany updates all documents matching filter.
func (s *SoftDeleteCollection) UpdateMany(ctx context.Context, filter bson.M, update interface{}, opts ...Option) (*mongo.UpdateResult, error) {
	cfg := applyOpts(opts)
	filter = addDeletedFilter(filter, cfg.includeDeleted)
	return s.coll.UpdateMany(ctx, filter, update)
}

// ReplaceOne replaces a single document matching filter, stripping deleted fields
// from the replacement.
func (s *SoftDeleteCollection) ReplaceOne(ctx context.Context, filter bson.M, replacement interface{}, opts ...Option) (*mongo.UpdateResult, error) {
	cfg := applyOpts(opts)
	filter = addDeletedFilter(filter, cfg.includeDeleted)
	repl, err := stripDeleteFields(replacement)
	if err != nil {
		return nil, err
	}
	return s.coll.ReplaceOne(ctx, filter, repl)
}

// ------------------------------------------------------------------
// Soft delete
// ------------------------------------------------------------------

// Delete soft-deletes a single document matching filter by setting
// deleted=true and deleted_at to the current UTC time.
func (s *SoftDeleteCollection) Delete(ctx context.Context, filter bson.M) (*mongo.UpdateResult, error) {
	update := bson.M{
		"$set": bson.M{
			softDeleteField:   true,
			softDeleteAtField: time.Now().UTC(),
		},
	}
	return s.coll.UpdateOne(ctx, filter, update)
}

// DeleteMany soft-deletes all documents matching filter.
func (s *SoftDeleteCollection) DeleteMany(ctx context.Context, filter bson.M) (*mongo.UpdateResult, error) {
	update := bson.M{
		"$set": bson.M{
			softDeleteField:   true,
			softDeleteAtField: time.Now().UTC(),
		},
	}
	return s.coll.UpdateMany(ctx, filter, update)
}

// ------------------------------------------------------------------
// Restore
// ------------------------------------------------------------------

// Restore un-deletes a single document matching filter by setting
// deleted=false and unsetting deleted_at.
func (s *SoftDeleteCollection) Restore(ctx context.Context, filter bson.M) (*mongo.UpdateResult, error) {
	update := bson.M{
		"$set": bson.M{
			softDeleteField: false,
		},
		"$unset": bson.M{
			softDeleteAtField: "",
		},
	}
	return s.coll.UpdateOne(ctx, filter, update)
}

// RestoreMany un-deletes all documents matching filter.
func (s *SoftDeleteCollection) RestoreMany(ctx context.Context, filter bson.M) (*mongo.UpdateResult, error) {
	update := bson.M{
		"$set": bson.M{
			softDeleteField: false,
		},
		"$unset": bson.M{
			softDeleteAtField: "",
		},
	}
	return s.coll.UpdateMany(ctx, filter, update)
}

// ------------------------------------------------------------------
// Hard delete (permanent)
// ------------------------------------------------------------------

// HardDelete permanently removes a single document matching filter.
func (s *SoftDeleteCollection) HardDelete(ctx context.Context, filter bson.M) (*mongo.DeleteResult, error) {
	return s.coll.DeleteOne(ctx, filter)
}

// HardDeleteMany permanently removes all documents matching filter.
func (s *SoftDeleteCollection) HardDeleteMany(ctx context.Context, filter bson.M) (*mongo.DeleteResult, error) {
	return s.coll.DeleteMany(ctx, filter)
}

// ------------------------------------------------------------------
// Passthrough / metadata
// ------------------------------------------------------------------

// Name returns the collection name.
func (s *SoftDeleteCollection) Name() string {
	return s.coll.Name()
}

// Database returns the database that contains the collection.
func (s *SoftDeleteCollection) Database() *mongo.Database {
	return s.coll.Database()
}

// ------------------------------------------------------------------
// Options
// ------------------------------------------------------------------

// Option configures a single SoftDeleteCollection operation.
type Option func(*optConfig)

type optConfig struct {
	includeDeleted bool
}

// WithIncludeDeleted includes soft-deleted documents in the operation.
func WithIncludeDeleted() Option {
	return func(c *optConfig) {
		c.includeDeleted = true
	}
}

func applyOpts(opts []Option) optConfig {
	var cfg optConfig
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// stripDeleteFields returns a bson.M with deleted and deleted_at removed.
// If the input is already bson.M it mutates a copy; otherwise it marshals
// and unmarshals to bson.M.
func stripDeleteFields(doc interface{}) (interface{}, error) {
	switch d := doc.(type) {
	case bson.M:
		out := make(bson.M, len(d))
		for k, v := range d {
			if k == softDeleteField || k == softDeleteAtField {
				continue
			}
			out[k] = v
		}
		return out, nil
	case map[string]interface{}:
		out := make(map[string]interface{}, len(d))
		for k, v := range d {
			if k == softDeleteField || k == softDeleteAtField {
				continue
			}
			out[k] = v
		}
		return out, nil
	default:
		b, err := bson.Marshal(doc)
		if err != nil {
			return nil, err
		}
		var m bson.M
		if err := bson.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		delete(m, softDeleteField)
		delete(m, softDeleteAtField)
		return m, nil
	}
}
