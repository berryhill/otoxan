package memory

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// setupMongo spins up a testcontainers MongoDB container and returns a client.
// It also sets MONGO_URI in the environment so the Python bridge can connect
// to the same instance.
func setupMongo(t *testing.T) *mongo.Client {
	t.Helper()
	ctx := context.Background()

	container, err := mongodb.Run(ctx, "mongo:7")
	if err != nil {
		t.Fatalf("failed to start mongodb container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(ctx)
	})

	uri, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}
	// Export so Python helper sees the same URI
	os.Setenv("MONGO_URI", uri)

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("failed to connect to mongodb: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Disconnect(ctx)
	})

	return client
}

// newTestStore returns a MemoryStore backed by a fresh test collection.
func newTestStore(t *testing.T, client *mongo.Client) *MemoryStore {
	t.Helper()
	db := client.Database("silas")
	coll := db.Collection("memory")
	return NewMemoryStore(coll, nil)
}

// makeMinimalMemory returns a memory with only required fields set.
func makeMinimalMemory(id, agentID, content string) *Memory {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return &Memory{
		MemoryID:  id,
		AgentID:   agentID,
		Content:   content,
		Type:      TypeObservation,
		CreatedAt: now,
		UpdatedAt: now,
		Tags:      []string{},
	}
}
