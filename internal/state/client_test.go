package state

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/mongodb"
)

// setupMongo spins up a testcontainers MongoDB container and returns its URI.
func setupMongo(t *testing.T) string {
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
	return uri
}

// TestClient verifies that OpenClient connects to a testcontainers MongoDB
// and that a second call returns the same instance.
func TestClient(t *testing.T) {
	uri := setupMongo(t)

	// Ensure a clean singleton state for this test.
	ResetClient()
	t.Cleanup(ResetClient)

	// First call should open a new client.
	c1, err := OpenClient(uri)
	if err != nil {
		t.Fatalf("first OpenClient failed: %v", err)
	}
	if c1 == nil {
		t.Fatal("first OpenClient returned nil client")
	}

	// Second call should return the exact same instance.
	c2, err := OpenClient(uri)
	if err != nil {
		t.Fatalf("second OpenClient failed: %v", err)
	}
	if c2 != c1 {
		t.Fatal("second OpenClient did not return the same instance")
	}

	// Verify the client is actually connected by pinging.
	ctx := context.Background()
	if err := c1.Ping(ctx, nil); err != nil {
		t.Fatalf("ping failed: %v", err)
	}
}
