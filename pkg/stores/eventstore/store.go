// Package eventstore provides a MongoDB-backed append-only event log for otoxan.
//
// A single Store serves both global audit events (otoxan_global.audit_events)
// and per-agent task events (otoxan_agent_<n>.task_events).  The caller picks
// the target collection via Scope when constructing the store.
package eventstore

import (
	"context"
	"fmt"
	"time"

	"github.com/silas/otoxan/internal/state"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Document model
// ------------------------------------------------------------------

// EventDoc is the canonical BSON shape for every event written by this store.
type EventDoc struct {
	EventID   string    `bson:"event_id" json:"event_id"`
	Type      string    `bson:"type" json:"type"`
	Timestamp time.Time `bson:"timestamp" json:"timestamp"`
	Actor     string    `bson:"actor" json:"actor"`
	Data      bson.M    `bson:"data" json:"data"`
}

// ------------------------------------------------------------------
// Scope — selects the DB / collection pair
// ------------------------------------------------------------------

// Scope tells the store which database and collection to target.
type Scope struct {
	DB         *mongo.Database
	Collection string
}

// GlobalAuditEvents returns a Scope pointing at otoxan_global.audit_events.
func GlobalAuditEvents(client *mongo.Client) Scope {
	return Scope{
		DB:         state.GlobalDB(client),
		Collection: "audit_events",
	}
}

// AgentTaskEvents returns a Scope pointing at otoxan_agent_<name>.task_events.
func AgentTaskEvents(client *mongo.Client, agentName string) (Scope, error) {
	db, err := state.AgentDB(client, agentName)
	if err != nil {
		return Scope{}, err
	}
	return Scope{
		DB:         db,
		Collection: "task_events",
	}, nil
}

// ------------------------------------------------------------------
// Store construction
// ------------------------------------------------------------------

// NewStore creates an event store for the given scope.
// It ensures required indexes on the target collection.
func NewStore(scope Scope) (*Store, error) {
	coll := scope.DB.Collection(scope.Collection)
	s := &Store{coll: coll}
	if err := s.ensureIndexes(context.Background()); err != nil {
		return nil, fmt.Errorf("ensure indexes: %w", err)
	}
	return s, nil
}

// Store is a MongoDB-backed append-only event log.
type Store struct {
	coll *mongo.Collection
}

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (s *Store) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "event_id", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		{Keys: bson.D{{Key: "type", Value: 1}, {Key: "timestamp", Value: 1}}},
		{Keys: bson.D{{Key: "timestamp", Value: 1}}},
		{Keys: bson.D{{Key: "actor", Value: 1}, {Key: "timestamp", Value: 1}}},
	}
	_, err := s.coll.Indexes().CreateMany(ctx, indexes)
	return err
}

// ------------------------------------------------------------------
// Append
// ------------------------------------------------------------------

// Append inserts a single event.  If doc.EventID is empty a ULID-style
// hex ID is generated.  Timestamp is set to UTC now when zero.
func (s *Store) Append(ctx context.Context, doc EventDoc) (*mongo.InsertOneResult, error) {
	if doc.EventID == "" {
		doc.EventID = generateEventID()
	}
	if doc.Timestamp.IsZero() {
		doc.Timestamp = time.Now().UTC()
	}
	return s.coll.InsertOne(ctx, doc)
}

// ------------------------------------------------------------------
// Tail — read newest N events
// ------------------------------------------------------------------

// TailOptions configures the Tail query.
type TailOptions struct {
	Limit int
	Since *time.Time // optional lower-bound timestamp (exclusive)
}

// Tail returns the most recent events ordered by timestamp descending.
func (s *Store) Tail(ctx context.Context, opts TailOptions) ([]EventDoc, error) {
	filter := bson.M{}
	if opts.Since != nil {
		filter["timestamp"] = bson.M{"$gt": *opts.Since}
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "timestamp", Value: -1}})
	if opts.Limit > 0 {
		findOpts.SetLimit(int64(opts.Limit))
	}

	cur, err := s.coll.Find(ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var docs []EventDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

// ------------------------------------------------------------------
// QueryByType — filter by event type, newest first
// ------------------------------------------------------------------

// QueryByTypeOptions configures the QueryByType query.
type QueryByTypeOptions struct {
	Type  string   // required event type
	Limit int      // max results
	Since *time.Time // optional lower-bound timestamp (exclusive)
}

// QueryByType returns events matching the given type ordered by timestamp descending.
func (s *Store) QueryByType(ctx context.Context, opts QueryByTypeOptions) ([]EventDoc, error) {
	filter := bson.M{"type": opts.Type}
	if opts.Since != nil {
		filter["timestamp"] = bson.M{"$gt": *opts.Since}
	}

	findOpts := options.Find().
		SetSort(bson.D{{Key: "timestamp", Value: -1}})
	if opts.Limit > 0 {
		findOpts.SetLimit(int64(opts.Limit))
	}

	cur, err := s.coll.Find(ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var docs []EventDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

// ------------------------------------------------------------------
// Utility helpers
// ------------------------------------------------------------------

func generateEventID() string {
	return fmt.Sprintf("evt_%s", generateHex(7))
}

func generateHex(n int) string {
	const hexChars = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = hexChars[time.Now().UnixNano()%int64(len(hexChars))]
	}
	return string(b)
}
