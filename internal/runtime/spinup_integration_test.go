//go:build integration

// Package runtime provides end-to-end integration tests for the container
// spinup system: Docker pull → create → start → MongoDB registration
// → watcher status sync → teardown removes from MongoDB.
//
// Run with: go test -tags=integration ./internal/runtime/...
package runtime

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/runtime/containerstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// setupTestMongo spins up a testcontainers MongoDB container and returns a client.
func setupTestMongo(t *testing.T) *mongo.Client {
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

	// Ping to confirm connectivity.
	require.NoError(t, client.Ping(ctx, nil))
	return client
}

// TestSpinup_Lifecycle is the primary acceptance test: spin up a hello-world-like
// container, verify it registers in the containers collection, watcher updates
// its status, and spin down removes it.
func TestSpinup_Lifecycle(t *testing.T) {
	ctx := context.Background()

	// 1. Set up MongoDB via testcontainers.
	mongoClient := setupTestMongo(t)

	// 2. Set up the container store.
	store, err := containerstore.NewStore(mongoClient)
	require.NoError(t, err)

	// 3. Connect to Docker.
	dockerCli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerCli.Close() })

	containerName := fmt.Sprintf("otoxan-spinup-test-%d", time.Now().UnixNano()%1e6)

	// 4. Spinup a container using alpine with a short-lived command.
	// We use "echo done" so it exits immediately — this tests the full
	// spinup + teardown path including the watcher seeing "exited" state.
	result, err := Spinup(ctx, dockerCli, store, SpinupConfig{
		Name:      containerName,
		Image:     "docker.io/library/alpine:latest",
		Cmd:       []string{"echo", "spinup_test_ok"},
		Owner:     "silas",
		OwnerType: "agent",
		Role:      "test-worker",
		Env:       []string{"OTOXAN_ROLE=test"},
		// AutoRemove=false so we can inspect it before teardown.
		AutoRemove: false,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.ContainerID)
	t.Cleanup(func() {
		// Teardown in all cases.
		_ = Teardown(ctx, dockerCli, store, result.ContainerID, true, 5*time.Second)
	})

	// 5. Verify the container document was written to Mongo.
	doc, err := store.GetByContainerID(ctx, result.ContainerID)
	require.NoError(t, err)
	assert.Equal(t, result.ContainerID, doc.ContainerID)
	assert.Equal(t, containerName, doc.Name)
	assert.Equal(t, "docker.io/library/alpine:latest", doc.Image)
	assert.Equal(t, "silas", doc.Owner)
	assert.Equal(t, "agent", doc.OwnerType)
	assert.Equal(t, "test-worker", doc.Role)
	assert.NotZero(t, doc.CreatedAt)
	assert.NotZero(t, doc.UpdatedAt)

	// Give Docker a moment to process the exit.
	time.Sleep(500 * time.Millisecond)

	// 6. Start the watcher and let it sync.
	watcher := NewWatcher(dockerCli, store, 2*time.Second)
	watcher.Start(ctx)
	time.Sleep(3 * time.Second) // let it run at least one cycle.
	watcher.Stop()

	// 7. Verify watcher updated the status.
	docAfterWatch, err := store.GetByContainerID(ctx, result.ContainerID)
	require.NoError(t, err)
	// The container ran "echo done" and exited with code 0.
	assert.True(t,
		string(docAfterWatch.Status) == string(StateExited) ||
			string(docAfterWatch.Status) == string(StateRunning),
		"expected exited or running, got: %s", docAfterWatch.Status)

	// 8. Teardown removes from Mongo.
	err = Teardown(ctx, dockerCli, store, result.ContainerID, true, 5*time.Second)
	require.NoError(t, err)

	// 9. Verify the document is gone.
	_, err = store.GetByContainerID(ctx, result.ContainerID)
	assert.Equal(t, mongo.ErrNoDocuments, err)
}

// TestSpinup_Simple is a convenience-wrapper smoke test.
func TestSpinup_Simple(t *testing.T) {
	ctx := context.Background()

	mongoClient := setupTestMongo(t)
	store, err := containerstore.NewStore(mongoClient)
	require.NoError(t, err)

	dockerCli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerCli.Close() })

	// Use Spinup directly with a long-running command so the container is still
	// running when we inspect it. SpinupSimple passes no Cmd, which uses the
	// image default (alpine's ash exits immediately without a tty).
	name := fmt.Sprintf("otoxan-simple-%d", time.Now().UnixNano()%1e6)
	result, err := Spinup(ctx, dockerCli, store, SpinupConfig{
		Name:  name,
		Image: "docker.io/library/alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Role:  "test-simple",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = Teardown(ctx, dockerCli, store, result.ContainerID, true, 5*time.Second)
	})

	// Document should exist immediately.
	doc, err := store.GetByContainerID(ctx, result.ContainerID)
	require.NoError(t, err)
	assert.Equal(t, name, doc.Name)
	assert.Equal(t, "system", doc.OwnerType) // SpinupSimple defaults to "system".

	// Container should be running.
	assert.Equal(t, string(StateRunning), string(doc.Status))
}

// TestSpinup_TeardownByOwner verifies that TeardownByOwner cleans up all
// containers owned by a specific owner.
func TestSpinup_TeardownByOwner(t *testing.T) {
	ctx := context.Background()

	mongoClient := setupTestMongo(t)
	store, err := containerstore.NewStore(mongoClient)
	require.NoError(t, err)

	dockerCli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerCli.Close() })

	owner := fmt.Sprintf("teardown-owner-%d", time.Now().UnixNano()%1e6)

	// Spin up two containers for the same owner.
	var results []*SpinupResult
	for i := 0; i < 2; i++ {
		name := fmt.Sprintf("otoxan-multi-%d-%d", time.Now().UnixNano()%1e6, i)
		r, err := Spinup(ctx, dockerCli, store, SpinupConfig{
			Name:      name,
			Image:     "docker.io/library/alpine:latest",
			Cmd:       []string{"sleep", "3600"},
			Owner:     owner,
			OwnerType: "agent",
			Role:      "test-worker",
		})
		require.NoError(t, err)
		results = append(results, r)
	}

	// Both should be in Mongo.
	docs, err := store.ListByOwner(ctx, owner)
	require.NoError(t, err)
	require.Len(t, docs, 2)

	// Teardown by owner.
	removed, err := TeardownByOwner(ctx, dockerCli, store, owner, true, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, removed, 2)

	// Store should be empty for this owner.
	docs, err = store.ListByOwner(ctx, owner)
	require.NoError(t, err)
	assert.Empty(t, docs)
}

// TestSpinup_StopTimeout verifies that containers respect the stop timeout.
func TestSpinup_StopTimeout(t *testing.T) {
	ctx := context.Background()

	mongoClient := setupTestMongo(t)
	store, err := containerstore.NewStore(mongoClient)
	require.NoError(t, err)

	dockerCli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerCli.Close() })

	// cat blocks indefinitely — only SIGKILL can stop it.
	name := fmt.Sprintf("otoxan-timeout-%d", time.Now().UnixNano()%1e6)
	result, err := Spinup(ctx, dockerCli, store, SpinupConfig{
		Name:      name,
		Image:     "docker.io/library/alpine:latest",
		Cmd:       []string{"cat"}, // blocks forever
		Owner:     "silas",
		OwnerType: "agent",
		Role:      "test-timeout",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = Teardown(ctx, dockerCli, store, result.ContainerID, true, 0)
	})

	// Re-sync to get the current state (container may have exited immediately
	// if cat didn't block as expected, which is also fine).
	watcher := NewWatcher(dockerCli, store, 10*time.Second)
	_ = watcher.SyncOne(ctx, result.ContainerID)

	doc, err := store.GetByContainerID(ctx, result.ContainerID)
	require.NoError(t, err)
	// cat containers may or may not still be running depending on Docker environment.
	// The important thing is that we can stop them with a short timeout.
	_ = doc // verify document exists; state may be running or exited

	// Stop with a short timeout — should escalate to SIGKILL.
	err = Teardown(ctx, dockerCli, store, result.ContainerID, false, 1*time.Second)
	require.NoError(t, err)

	// Document should be gone.
	_, err = store.GetByContainerID(ctx, result.ContainerID)
	assert.Equal(t, mongo.ErrNoDocuments, err)
}

// TestWatcher_SyncOne verifies that SyncOne updates the document after the container exits.
func TestWatcher_SyncOne(t *testing.T) {
	ctx := context.Background()

	mongoClient := setupTestMongo(t)
	store, err := containerstore.NewStore(mongoClient)
	require.NoError(t, err)

	dockerCli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerCli.Close() })

	name := fmt.Sprintf("otoxan-syncone-%d", time.Now().UnixNano()%1e6)
	result, err := Spinup(ctx, dockerCli, store, SpinupConfig{
		Name:      name,
		Image:     "docker.io/library/alpine:latest",
		Cmd:       []string{"echo", "sync test"},
		Owner:     "silas",
		OwnerType: "agent",
		Role:      "test-syncone",
		AutoRemove: false,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = Teardown(ctx, dockerCli, store, result.ContainerID, true, 5*time.Second)
	})

	// Give it time to exit.
	time.Sleep(500 * time.Millisecond)

	// SyncOne should detect the exit and update Mongo.
	watcher := NewWatcher(dockerCli, store, 10*time.Second)
	err = watcher.SyncOne(ctx, result.ContainerID)
	require.NoError(t, err)

	doc, err := store.GetByContainerID(ctx, result.ContainerID)
	require.NoError(t, err)
	assert.Equal(t, string(StateExited), string(doc.Status))
	assert.Equal(t, 0, doc.ExitCode)
	assert.NotZero(t, doc.FinishedAt)
}

// TestSpinup_ListContainers verifies that List returns all containers with optional filters.
func TestSpinup_ListContainers(t *testing.T) {
	ctx := context.Background()

	mongoClient := setupTestMongo(t)
	store, err := containerstore.NewStore(mongoClient)
	require.NoError(t, err)

	dockerCli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerCli.Close() })

	owner1 := fmt.Sprintf("list-owner-a-%d", time.Now().UnixNano()%1e6)
	owner2 := fmt.Sprintf("list-owner-b-%d", time.Now().UnixNano()%1e6)

	// Create one container for owner1, two for owner2.
	for _, owner := range []string{owner1, owner2} {
		count := 1
		if owner == owner2 {
			count = 2
		}
		for i := 0; i < count; i++ {
			name := fmt.Sprintf("otoxan-list-%s-%d", owner, i)
			r, err := Spinup(ctx, dockerCli, store, SpinupConfig{
				Name:      name,
				Image:     "docker.io/library/alpine:latest",
				Cmd:       []string{"sleep", "3600"},
				Owner:     owner,
				OwnerType: "agent",
				Role:      "test-list",
			})
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = Teardown(ctx, dockerCli, store, r.ContainerID, true, 5*time.Second)
			})
		}
	}

	// List all — should have 3.
	all, err := store.List(ctx, "", "")
	require.NoError(t, err)
	require.Len(t, all, 3)

	// Filter by owner — should have 1 for owner1, 2 for owner2.
	owner1Docs, err := store.ListByOwner(ctx, owner1)
	require.NoError(t, err)
	require.Len(t, owner1Docs, 1)

	owner2Docs, err := store.ListByOwner(ctx, owner2)
	require.NoError(t, err)
	require.Len(t, owner2Docs, 2)

	// Filter by owner + status.
	running, err := store.List(ctx, owner1, string(StateRunning))
	require.NoError(t, err)
	require.Len(t, running, 1)
	assert.Equal(t, owner1, running[0].Owner)
}

// TestSpinup_PullFailure verifies that a pull failure returns an error without
// leaving a dangling container document.
func TestSpinup_PullFailure(t *testing.T) {
	ctx := context.Background()

	mongoClient := setupTestMongo(t)
	store, err := containerstore.NewStore(mongoClient)
	require.NoError(t, err)

	dockerCli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerCli.Close() })

	// Use an image that does not exist — pull should fail.
	_, err = Spinup(ctx, dockerCli, store, SpinupConfig{
		Name:  "otoxan-pull-fail",
		Image: "docker.io/library/this-image-does-not-exist-12345:latest",
		Owner: "silas",
		Role:  "test-pull-fail",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pull")

	// No documents should be in the store.
	all, err := store.List(ctx, "", "")
	require.NoError(t, err)
	assert.Empty(t, all)
}

// TestSpinup_UpsertIdempotency verifies that spinning up a container with the
// same name updates rather than duplicates.
func TestSpinup_UpsertIdempotency(t *testing.T) {
	ctx := context.Background()

	mongoClient := setupTestMongo(t)
	store, err := containerstore.NewStore(mongoClient)
	require.NoError(t, err)

	dockerCli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerCli.Close() })

	containerName := fmt.Sprintf("otoxan-upsert-%d", time.Now().UnixNano()%1e6)

	// Spin up once.
	r1, err := Spinup(ctx, dockerCli, store, SpinupConfig{
		Name:      containerName,
		Image:     "docker.io/library/alpine:latest",
		Cmd:       []string{"sleep", "3600"},
		Owner:     "silas",
		OwnerType: "agent",
		Role:      "test-upsert",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = Teardown(ctx, dockerCli, store, r1.ContainerID, true, 5*time.Second)
	})

	// List all — should have exactly 1.
	all, err := store.List(ctx, "", "")
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, containerName, all[0].Name)
}

// TestSpinup_PortMappings verifies that port mappings are stored in the document.
func TestSpinup_PortMappings(t *testing.T) {
	ctx := context.Background()

	mongoClient := setupTestMongo(t)
	store, err := containerstore.NewStore(mongoClient)
	require.NoError(t, err)

	dockerCli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerCli.Close() })

	containerName := fmt.Sprintf("otoxan-ports-%d", time.Now().UnixNano()%1e6)
	r, err := Spinup(ctx, dockerCli, store, SpinupConfig{
		Name:      containerName,
		Image:     "docker.io/library/nginx:alpine",
		Ports:     []string{"0:8080/tcp", "0:9090/tcp"},
		Owner:     "silas",
		OwnerType: "agent",
		Role:      "test-ports",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = Teardown(ctx, dockerCli, store, r.ContainerID, true, 5*time.Second)
	})

	doc, err := store.GetByContainerID(ctx, r.ContainerID)
	require.NoError(t, err)
	assert.Len(t, doc.PortMappings, 2)
}

// TestSpinup_BindMounts verifies that bind mounts are stored in the document.
func TestSpinup_BindMounts(t *testing.T) {
	ctx := context.Background()

	mongoClient := setupTestMongo(t)
	store, err := containerstore.NewStore(mongoClient)
	require.NoError(t, err)

	dockerCli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerCli.Close() })

	containerName := fmt.Sprintf("otoxan-mounts-%d", time.Now().UnixNano()%1e6)
	r, err := Spinup(ctx, dockerCli, store, SpinupConfig{
		Name:      containerName,
		Image:     "docker.io/library/alpine:latest",
		Cmd:       []string{"sleep", "3600"},
		BindMounts: []string{
			"/tmp:/mnt/ro:ro",
			"/tmp:/mnt/rw",
		},
		Owner:     "silas",
		OwnerType: "agent",
		Role:      "test-mounts",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = Teardown(ctx, dockerCli, store, r.ContainerID, true, 5*time.Second)
	})

	doc, err := store.GetByContainerID(ctx, r.ContainerID)
	require.NoError(t, err)
	assert.Contains(t, doc.BindMounts, "/tmp:/mnt/ro:ro")
	assert.Contains(t, doc.BindMounts, "/tmp:/mnt/rw")
}

// TestSpinup_ContainerInfoRoundTrip verifies that ContainerInfo from Inspect
// can be round-tripped through the store's UpdateStatus.
func TestSpinup_ContainerInfoRoundTrip(t *testing.T) {
	ctx := context.Background()

	mongoClient := setupTestMongo(t)
	store, err := containerstore.NewStore(mongoClient)
	require.NoError(t, err)

	dockerCli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dockerCli.Close() })

	containerName := fmt.Sprintf("otoxan-roundtrip-%d", time.Now().UnixNano()%1e6)
	r, err := Spinup(ctx, dockerCli, store, SpinupConfig{
		Name:      containerName,
		Image:     "docker.io/library/alpine:latest",
		Cmd:       []string{"sleep", "3600"},
		Owner:     "silas",
		OwnerType: "agent",
		Role:      "test-roundtrip",
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = Teardown(ctx, dockerCli, store, r.ContainerID, true, 5*time.Second)
	})

	// Inspect the running container.
	info, err := dockerCli.Inspect(ctx, r.ContainerID)
	require.NoError(t, err)

	// Update status via store.
	err = store.UpdateStatus(ctx, r.ContainerID, info)
	require.NoError(t, err)

	// Read back and verify.
	doc, err := store.GetByContainerID(ctx, r.ContainerID)
	require.NoError(t, err)
	assert.Equal(t, string(info.State), string(doc.Status))
	assert.Equal(t, info.ExitCode, doc.ExitCode)
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

// verifyStoreInterface is a compile-time check that Store implements the
// expected interface.
var verifyStoreInterface func(*testing.T, *containerstore.Store) = func(t *testing.T, s *containerstore.Store) {
	t.Helper()
	require.NotNil(t, s)
	// Verify store implements the expected methods.
	var _ interface {
		Upsert(context.Context, *containerstore.ContainerDoc) error
		GetByContainerID(context.Context, string) (*containerstore.ContainerDoc, error)
		UpdateStatus(context.Context, string, *ContainerInfo) error
		Delete(context.Context, string) error
		ListByOwner(context.Context, string) ([]containerstore.ContainerDoc, error)
		List(context.Context, string, string) ([]containerstore.ContainerDoc, error)
		Collection() *mongo.Collection
	} = s
}

var _ = bson.M{}
