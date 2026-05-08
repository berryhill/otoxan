// Package directives provides a MongoDB-backed store for directive documents with
// soft-delete semantics.
package directives

import (
	"context"
	"time"

	"github.com/silas/otoxan/internal/softdelete"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Directive is the canonical BSON shape for an agent directive document.
type Directive struct {
	DirectiveID string    `bson:"directive_id" json:"directive_id"`
	Title       string    `bson:"title" json:"title"`
	Content     string    `bson:"content" json:"content"`
	Category    string    `bson:"category" json:"category"`
	Priority    int       `bson:"priority" json:"priority"`
	Enabled     bool      `bson:"enabled" json:"enabled"`
	Tags        []string  `bson:"tags" json:"tags"`
	Owner       string    `bson:"owner" json:"owner"`
	CreatedAt   time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time `bson:"updated_at" json:"updated_at"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// NewDirectiveStore creates a DirectiveStore backed by the given MongoDB collection.
// It ensures required indexes on the directives collection.
func NewDirectiveStore(coll *mongo.Collection) *DirectiveStore {
	sd := softdelete.NewSoftDelete(coll)
	ds := &DirectiveStore{sd: sd}
	_ = ds.ensureIndexes(context.Background())
	return ds
}

// DirectiveStore is a MongoDB-backed store for directive documents with soft-delete
// semantics. It mirrors the Python directivestore.DirectiveStore CRUD surface.
type DirectiveStore struct {
	sd *softdelete.SoftDeleteCollection
}

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (s *DirectiveStore) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "directive_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "category", Value: 1}}},
		{Keys: bson.D{{Key: "priority", Value: 1}}},
		{Keys: bson.D{{Key: "enabled", Value: 1}}},
		{Keys: bson.D{{Key: "updated_at", Value: 1}}},
	}
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// ------------------------------------------------------------------
// CRUD
// ------------------------------------------------------------------

// Create inserts a new directive document. The caller must set DirectiveID and Title.
func (s *DirectiveStore) Create(ctx context.Context, d *Directive) (*mongo.InsertOneResult, error) {
	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = now
	}
	if d.Category == "" {
		d.Category = "general"
	}
	if d.Tags == nil {
		d.Tags = []string{}
	}
	// Default enabled=true (matches Python directivestore behavior).
	// Caller can explicitly disable via Update after Create.
	d.Enabled = true
	return s.sd.InsertOne(ctx, d)
}

// Get retrieves a single directive by directive_id. Returns mongo.ErrNoDocuments if
// not found (or soft-deleted).
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

// GetWithDeleted retrieves a directive including soft-deleted ones.
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

// Update patches fields of an existing directive. Sets updated_at automatically.
func (s *DirectiveStore) Update(ctx context.Context, directiveID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"directive_id": directiveID}, bson.M{"$set": updates})
}

// Upsert inserts or updates a directive.
func (s *DirectiveStore) Upsert(ctx context.Context, d *Directive) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = now
	}
	if d.Category == "" {
		d.Category = "general"
	}
	if d.Tags == nil {
		d.Tags = []string{}
	}

	setDoc := bson.M{
		"directive_id": d.DirectiveID,
		"title":        d.Title,
		"content":      d.Content,
		"category":     d.Category,
		"priority":     d.Priority,
		"enabled":      d.Enabled,
		"tags":         d.Tags,
		"updated_at":   d.UpdatedAt,
	}
	insertDoc := bson.M{
		"created_at": d.CreatedAt,
		"owner":      d.Owner,
	}
	return s.sd.Database().Collection(s.sd.Name()).UpdateOne(ctx, bson.M{"directive_id": d.DirectiveID}, bson.M{
		"$set":         setDoc,
		"$setOnInsert": insertDoc,
	}, options.UpdateOne().SetUpsert(true))
}

// Delete soft-deletes a directive by directive_id.
func (s *DirectiveStore) Delete(ctx context.Context, directiveID string) (*mongo.UpdateResult, error) {
	return s.sd.Delete(ctx, bson.M{"directive_id": directiveID})
}

// Restore un-deletes a soft-deleted directive.
func (s *DirectiveStore) Restore(ctx context.Context, directiveID string) (*mongo.UpdateResult, error) {
	return s.sd.Restore(ctx, bson.M{"directive_id": directiveID})
}

// HardDelete permanently removes a directive.
func (s *DirectiveStore) HardDelete(ctx context.Context, directiveID string) (*mongo.DeleteResult, error) {
	return s.sd.HardDelete(ctx, bson.M{"directive_id": directiveID})
}

// ------------------------------------------------------------------
// List / Query
// ------------------------------------------------------------------

// ListOptions configures the List query.
type ListOptions struct {
	Category       string
	EnabledOnly    bool
	Limit          int
	IncludeDeleted bool
}

// List returns directives matching the provided filters, sorted by priority
// descending then updated_at descending.
func (s *DirectiveStore) List(ctx context.Context, opts ListOptions) ([]Directive, error) {
	filter := bson.M{}
	if opts.Category != "" {
		filter["category"] = opts.Category
	}
	if opts.EnabledOnly {
		filter["enabled"] = true
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

	// Sort by priority desc, updated_at desc, limit in-memory
	sortDirectives(directives)
	if opts.Limit > 0 && len(directives) > opts.Limit {
		directives = directives[:opts.Limit]
	}
	return directives, nil
}

// Count returns the number of directives matching the filter.
func (s *DirectiveStore) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.sd.CountDocuments(ctx, filter)
}

// sortDirectives sorts by priority descending then updated_at descending.
func sortDirectives(directives []Directive) {
	for i := 0; i < len(directives); i++ {
		for j := i + 1; j < len(directives); j++ {
			if directives[j].Priority > directives[i].Priority ||
				(directives[j].Priority == directives[i].Priority && directives[j].UpdatedAt.After(directives[i].UpdatedAt)) {
				directives[i], directives[j] = directives[j], directives[i]
			}
		}
	}
}

// ------------------------------------------------------------------
// Collection passthrough
// ------------------------------------------------------------------

// Name returns the underlying collection name.
func (s *DirectiveStore) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *DirectiveStore) Database() *mongo.Database {
	return s.sd.Database()
}
