// Package containerstore provides a MongoDB-backed store for container lifecycle
// state. Every container that Xander spins up is registered in otoxan_global.containers
// so that the fleet is queryable from otoxan recall and dashboards.
//
// The watcher (internal/runtime/watcher.go) keeps container state in sync with
// the Docker daemon by polling and patching the status field.
package containerstore

import (
	"context"
	"fmt"
	"time"

	"github.com/silas/otoxan/internal/runtime/types"
	"github.com/silas/otoxan/internal/state"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Store manages container lifecycle documents in the global containers collection.
type Store struct {
	client *mongo.Client
	coll   *mongo.Collection
}

// NewStore creates a Store backed by the global containers collection.
// It ensures required indexes on first use.
func NewStore(client *mongo.Client) (*Store, error) {
	coll := state.GlobalDB(client).Collection("containers")
	s := &Store{client: client, coll: coll}
	if err := s.ensureIndexes(context.Background()); err != nil {
		return nil, fmt.Errorf("ensure indexes: %w", err)
	}
	return s, nil
}

// ensureIndexes creates indexes for the containers collection.
func (s *Store) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "container_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "owner", Value: 1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "created_at", Value: 1}}},
	}
	_, err := s.coll.Indexes().CreateMany(ctx, indexes)
	return err
}

// ContainerDoc is the MongoDB document shape for a managed container.
// It is persisted in otoxan_global.containers.
// The container_id field is the business key; _id is managed by MongoDB.
type ContainerDoc struct {
	ContainerID  string    `bson:"container_id"`           // Docker container ID (short form, 12 chars)
	Name         string    `bson:"name"`                  // Container name (without leading slash)
	Image        string    `bson:"image"`                 // Image reference used to create this container
	Owner        string    `bson:"owner"`                 // Owner agent or system identifier
	OwnerType    string    `bson:"owner_type"`            // "agent" or "system"
	Role         string    `bson:"role"`                  // e.g. "indexer", "dispatch_worker", "companion"
	Status       string    `bson:"status"`                // runtime.ContainerState
	ExitCode     int       `bson:"exit_code,omitempty"`
	StartedAt    time.Time `bson:"started_at,omitempty"`
	FinishedAt   time.Time `bson:"finished_at,omitempty"`
	CreatedAt    time.Time `bson:"created_at"`
	UpdatedAt    time.Time `bson:"updated_at"`
	BindMounts   []string  `bson:"bind_mounts,omitempty"`
	PortMappings []string  `bson:"port_mappings,omitempty"`
}

// Upsert registers or updates a container in the collection.
// It uses container_id as the unique key.
func (s *Store) Upsert(ctx context.Context, doc *ContainerDoc) error {
	doc.UpdatedAt = time.Now().UTC()
	filter := bson.M{"container_id": doc.ContainerID}
	update := bson.M{"$set": doc}
	opts := options.UpdateOne().SetUpsert(true)
	_, err := s.coll.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		return fmt.Errorf("containerstore: upsert %s: %w", doc.ContainerID, err)
	}
	return nil
}

// GetByContainerID retrieves a container document by its Docker container ID.
func (s *Store) GetByContainerID(ctx context.Context, containerID string) (*ContainerDoc, error) {
	var doc ContainerDoc
	err := s.coll.FindOne(ctx, bson.M{"container_id": containerID}).Decode(&doc)
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// UpdateStatus patches only the status, exit_code, started_at, and finished_at fields.
// This is the primary update path used by the watcher.
func (s *Store) UpdateStatus(ctx context.Context, containerID string, info *types.ContainerInfo) error {
	filter := bson.M{"container_id": containerID}
	update := bson.M{
		"$set": bson.M{
			"status":      string(info.State),
			"exit_code":   info.ExitCode,
			"started_at":   info.StartedAt,
			"finished_at":  info.FinishedAt,
			"updated_at":   time.Now().UTC(),
		},
	}
	_, err := s.coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return fmt.Errorf("containerstore: update status %s: %w", containerID, err)
	}
	return nil
}

// Delete removes a container document by container ID.
func (s *Store) Delete(ctx context.Context, containerID string) error {
	_, err := s.coll.DeleteOne(ctx, bson.M{"container_id": containerID})
	if err != nil {
		return fmt.Errorf("containerstore: delete %s: %w", containerID, err)
	}
	return nil
}

// ListByOwner returns all container documents for a given owner.
func (s *Store) ListByOwner(ctx context.Context, owner string) ([]ContainerDoc, error) {
	cur, err := s.coll.Find(ctx, bson.M{"owner": owner})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var docs []ContainerDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

// List returns all container documents, optionally filtered by owner or status.
func (s *Store) List(ctx context.Context, owner string, status string) ([]ContainerDoc, error) {
	filter := bson.M{}
	if owner != "" {
		filter["owner"] = owner
	}
	if status != "" {
		filter["status"] = status
	}
	cur, err := s.coll.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var docs []ContainerDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	return docs, nil
}

// Collection returns the underlying collection for direct access.
func (s *Store) Collection() *mongo.Collection {
	return s.coll
}
