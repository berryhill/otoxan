package main

import (
	"context"
	"os"
	"testing"

	"github.com/silas/otoxan/internal/state"
	"github.com/silas/otoxan/pkg/stores/agentregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// setupMongo spins up a testcontainers MongoDB container and returns a client.
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

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("failed to connect to mongodb: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Disconnect(ctx)
	})

	return client
}

// TestDBDrop_WithoutYes_ExitsNonZero verifies that `db drop <agent>` without --yes exits non-zero.
func TestDBDrop_WithoutYes_ExitsNonZero(t *testing.T) {
	cmd := newDBDropCmd()
	cmd.SetArgs([]string{"testagent"})

	// Capture stderr
	oldStderr := os.Stderr
	_, w, _ := os.Pipe()
	os.Stderr = w

	err := cmd.Execute()

	w.Close()
	os.Stderr = oldStderr

	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing --yes flag")
}

// TestDBDrop_WithYes_DropsAgentDBAndRegistry verifies that `db drop <agent> --yes`
// drops the per-agent database and removes the agent from the global registry.
func TestDBDrop_WithYes_DropsAgentDBAndRegistry(t *testing.T) {
	ctx := context.Background()
	client, uri := setupMongoWithURI(t)

	// Register an agent so we have something to drop.
	regStore, err := agentregistry.NewStore(client)
	require.NoError(t, err)

	_, err = regStore.Register(ctx, "testagent", "admin")
	require.NoError(t, err)

	// Verify the per-agent DB exists.
	agentDB := client.Database("otoxan_agent_testagent")
	colls, err := agentDB.ListCollectionNames(ctx, bson.M{})
	require.NoError(t, err)
	require.NotEmpty(t, colls)

	// Reset singleton and prime it with the testcontainers URI.
	state.ResetClient()
	_, err = state.OpenClient(uri)
	require.NoError(t, err)

	// Pass the mongo URI via env so runDBDrop can resolve it.
	t.Setenv("OTOXAN_MONGO_URI", uri)

	cmd := newDBDropCmd()
	cmd.SetArgs([]string{"testagent", "--yes"})
	err = cmd.Execute()
	require.NoError(t, err)

	// Verify the per-agent database is gone.
	dbNames, err := client.ListDatabaseNames(ctx, bson.M{})
	require.NoError(t, err)
	for _, name := range dbNames {
		assert.NotEqual(t, "otoxan_agent_testagent", name, "agent DB should be dropped")
	}

	// Verify the agent doc is gone from the global registry.
	_, err = regStore.GetWithDeleted(ctx, "testagent")
	assert.Equal(t, mongo.ErrNoDocuments, err)
}

// setupMongoWithURI spins up a testcontainers MongoDB container and returns both
// the connected client and the connection URI.
func setupMongoWithURI(t *testing.T) (*mongo.Client, string) {
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

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("failed to connect to mongodb: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Disconnect(ctx)
	})

	return client, uri
}
