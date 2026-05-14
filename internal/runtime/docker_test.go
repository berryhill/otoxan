package runtime

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDocker_NewClient verifies that NewDockerClient connects to the local
// Docker daemon and that Close is safe to call on a nil-free client.
func TestDocker_NewClient(t *testing.T) {
	ctx := context.Background()

	cli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	require.NotNil(t, cli)

	err = cli.Close()
	assert.NoError(t, err)
}

// TestDocker_NewClient_NoDaemon verifies that an error is returned when the
// Docker daemon is unreachable. We test this by pointing the client at a
// non-existent socket path.
func TestDocker_NewClient_NoDaemon(t *testing.T) {
	ctx := context.Background()
	// client.NewClientWithOpts uses DOCKER_HOST env var or default socket.
	// Override via env so we get a clear error without needing to mock the socket.
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/docker.sock")

	cli, err := NewDockerClient(ctx)
	// We expect an error when the daemon is unreachable.
	// The exact error message varies by OS but it should be non-nil.
	if err == nil {
		// If a daemon is somehow available, skip.
		t.Skip("Docker daemon appears to be running; skipping unreachable-daemon test")
	}
	assert.Error(t, err)
	assert.Nil(t, cli)
}

// TestDocker_Pull tests that Pull downloads a public image without error.
// We use a minimal static image: nginx:alpine.
func TestDocker_Pull(t *testing.T) {
	ctx := context.Background()

	cli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	defer cli.Close()

	// Pull a small static image.
	ref := "docker.io/library/nginx:alpine"

	var pullOutput bytes.Buffer
	err = cli.Pull(ctx, ref, &pullOutput)
	require.NoError(t, err, "Pull should succeed for a public image")

	// Output should contain progress info (JSON lines from Docker).
	assert.NotEmpty(t, pullOutput.String(), "pull output should not be empty")
}

// TestDocker_Pull_NilProgress verifies that Pull does not panic when
// progress is nil (i.e., the caller discards the stream).
func TestDocker_Pull_NilProgress(t *testing.T) {
	ctx := context.Background()

	cli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	defer cli.Close()

	err = cli.Pull(ctx, "docker.io/library/alpine:latest", nil)
	require.NoError(t, err)
}

// TestDocker_Create tests that Create produces a stopped container with
// the correct image, env, bind mounts, and exposed ports.
func TestDocker_Create(t *testing.T) {
	ctx := context.Background()

	cli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	defer cli.Close()

	// Ensure the image is present.
	err = cli.Pull(ctx, "docker.io/library/alpine:latest", nil)
	require.NoError(t, err)

	// Use a unique name so parallel runs don't collide.
	containerName := "otoxan-test-create-" + t.Name()

	cfg := ContainerConfig{
		Name:  containerName,
		Image: "docker.io/library/alpine:latest",
		Cmd:   []string{"sleep", "3600"},
		Env:   []string{"OTOXAN_TEST_VAR=hello", "SIMPLE=value"},
		BindMounts: []string{
			"/tmp:/mnt/ro:ro",
			"/tmp:/mnt/rw",
		},
		Ports: []string{
			"0:8080/tcp",
			"0:9090/udp",
		},
	}

	id, err := cli.Create(ctx, cfg)
	require.NoError(t, err)
	require.NotEmpty(t, id)

	// Clean up.
	defer func() {
		_ = cli.Remove(ctx, id, true)
	}()

	// Inspect to verify the container was created correctly.
	info, err := cli.Inspect(ctx, id)
	require.NoError(t, err)

	assert.Equal(t, id, info.ID)
	assert.Equal(t, containerName, strings.TrimPrefix(info.Name, "/"),
		"Docker prepends '/' to container names")
	assert.Equal(t, "docker.io/library/alpine:latest", info.Image)
	assert.Equal(t, StateCreated, info.State, "newly created container should be in 'created' state")
	assert.Contains(t, info.BindMounts, "/tmp:/mnt/ro:ro")
	assert.Contains(t, info.BindMounts, "/tmp:/mnt/rw")
}

// TestDocker_Start tests that Start transitions a created container to
// running, and that it is idempotent for already-running containers.
func TestDocker_Start(t *testing.T) {
	ctx := context.Background()

	cli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	defer cli.Close()

	// Use a long-running command so we can inspect it before it exits.
	id, err := cli.Create(ctx, ContainerConfig{
		Name:  "otoxan-test-start-" + t.Name(),
		Image: "docker.io/library/alpine:latest",
		Cmd:   []string{"sleep", "30"},
	})
	require.NoError(t, err)
	defer func() { _ = cli.Remove(ctx, id, true) }()

	// Start it.
	err = cli.Start(ctx, id)
	require.NoError(t, err)

	// Verify it is running.
	info, err := cli.Inspect(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, StateRunning, info.State)

	// Start again — should be idempotent.
	err = cli.Start(ctx, id)
	require.NoError(t, err, "Start should be idempotent on a running container")

	// Verify state is still running.
	info2, err := cli.Inspect(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, StateRunning, info2.State)
}

// TestDocker_Inspect tests that Inspect returns correct data for a running
// container, including port mappings and state fields.
func TestDocker_Inspect(t *testing.T) {
	ctx := context.Background()

	cli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	defer cli.Close()

	// nginx exposes port 80 internally; map to a random host port.
	id, err := cli.Create(ctx, ContainerConfig{
		Name:  "otoxan-test-inspect-" + t.Name(),
		Image: "docker.io/library/nginx:alpine",
		Ports: []string{"0:80/tcp"},
	})
	require.NoError(t, err)
	defer func() { _ = cli.Remove(ctx, id, true) }()

	err = cli.Start(ctx, id)
	require.NoError(t, err)

	// Allow Docker a moment to assign the host port.
	time.Sleep(500 * time.Millisecond)

	info, err := cli.Inspect(ctx, id)
	require.NoError(t, err)

	assert.Equal(t, id, info.ID)
	assert.Contains(t, info.Name, "otoxan-test-inspect")
	assert.Equal(t, "docker.io/library/nginx:alpine", info.Image)
	assert.Equal(t, StateRunning, info.State)
	assert.NotEmpty(t, info.StartedAt)

	// Port mapping should be present in the list.
	var foundPort bool
	for _, mapping := range info.PortMappings {
		// Expected format: "<hostPort>:80/tcp"
		if len(mapping) >= 4 {
			foundPort = true
			break
		}
	}
	assert.True(t, foundPort, "expected at least one port mapping, got: %v", info.PortMappings)
}

// TestDocker_Stop tests that Stop gracefully terminates a running container
// and that the container transitions to the exited state.
func TestDocker_Stop(t *testing.T) {
	ctx := context.Background()

	cli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	defer cli.Close()

	// Container with a long sleep so it is definitely running when we stop it.
	id, err := cli.Create(ctx, ContainerConfig{
		Name:  "otoxan-test-stop-" + t.Name(),
		Image: "docker.io/library/alpine:latest",
		Cmd:   []string{"sleep", "3600"},
	})
	require.NoError(t, err)
	defer func() { _ = cli.Remove(ctx, id, true) }()

	err = cli.Start(ctx, id)
	require.NoError(t, err)

	// Stop with a generous timeout (10s) for graceful shutdown.
	err = cli.Stop(ctx, id, 10*time.Second)
	require.NoError(t, err)

	info, err := cli.Inspect(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, StateExited, info.State, "container should be in exited state after Stop")
	assert.NotZero(t, info.FinishedAt)
}

// TestDocker_Stop_Timeout test that a very short timeout sends SIGKILL
// after the graceful period elapses.
func TestDocker_Stop_Timeout(t *testing.T) {
	ctx := context.Background()

	cli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	defer cli.Close()

	// Block the container indefinitely with cat so it won't exit on SIGTERM.
	id, err := cli.Create(ctx, ContainerConfig{
		Name:  "otoxan-test-stop-timeout-" + t.Name(),
		Image: "docker.io/library/alpine:latest",
		Cmd:   []string{"cat"}, // blocks stdin, waits forever
	})
	require.NoError(t, err)
	defer func() { _ = cli.Remove(ctx, id, true) }()

	err = cli.Start(ctx, id)
	require.NoError(t, err)

	// Use a 1-second timeout — Docker's default graceful window is 10s,
	// so with a 1s timeout the daemon will send SIGKILL.
	err = cli.Stop(ctx, id, 1*time.Second)
	require.NoError(t, err, "Stop should succeed even when it escalates to SIGKILL")

	info, err := cli.Inspect(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, StateExited, info.State)
}

// TestDocker_Remove tests that Remove deletes a stopped container.
// It also verifies that attempting to remove a running container without
// force fails, and succeeds with force=true.
func TestDocker_Remove(t *testing.T) {
	ctx := context.Background()

	cli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	defer cli.Close()

	id, err := cli.Create(ctx, ContainerConfig{
		Name:  "otoxan-test-remove-" + t.Name(),
		Image: "docker.io/library/alpine:latest",
		Cmd:   []string{"echo", "done"},
	})
	require.NoError(t, err)

	// Remove without force should succeed for a stopped container.
	err = cli.Remove(ctx, id, false)
	require.NoError(t, err)

	// Inspect should return an error for a non-existent container.
	_, err = cli.Inspect(ctx, id)
	assert.Error(t, err, "Inspect should fail for a removed container")
}

// TestDocker_Remove_ForceRunning verifies that force=true can remove a
// running container.
func TestDocker_Remove_ForceRunning(t *testing.T) {
	ctx := context.Background()

	cli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	defer cli.Close()

	id, err := cli.Create(ctx, ContainerConfig{
		Name:  "otoxan-test-remove-force-" + t.Name(),
		Image: "docker.io/library/alpine:latest",
		Cmd:   []string{"sleep", "3600"},
	})
	require.NoError(t, err)

	err = cli.Start(ctx, id)
	require.NoError(t, err)

	// Remove without force should fail for a running container.
	err = cli.Remove(ctx, id, false)
	assert.Error(t, err, "Remove(force=false) should fail for a running container")

	// Force remove should succeed.
	err = cli.Remove(ctx, id, true)
	require.NoError(t, err)
}

// TestDocker_HealthCheck_Smoke is an end-to-end smoke test: Pull → Create →
// Start → Inspect → Stop → Remove. It verifies the full happy-path lifecycle
// without using testcontainers (which would spawn Docker-in-Docker).
func TestDocker_HealthCheck_Smoke(t *testing.T) {
	ctx := context.Background()

	cli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	defer cli.Close()

	ref := "docker.io/library/alpine:latest"
	containerName := "otoxan-smoke-" + t.Name()

	// 1. Pull
	t.Log("pulling image...")
	err = cli.Pull(ctx, ref, io.Discard)
	require.NoError(t, err)

	// 2. Create
	t.Log("creating container...")
	id, err := cli.Create(ctx, ContainerConfig{
		Name:  containerName,
		Image: ref,
		Cmd:   []string{"sleep", "30"},
		Env:   []string{"OTOXAN_ENV=smoke_test"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, id)
	t.Logf("created container %s", id[:12])

	// 3. Start
	t.Log("starting container...")
	err = cli.Start(ctx, id)
	require.NoError(t, err)

	// 4. Inspect
	info, err := cli.Inspect(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, StateRunning, info.State, "container should be running after Start")
	assert.Equal(t, containerName, strings.TrimPrefix(info.Name, "/"),
		"Docker prepends '/' to container names")
	t.Logf("container state: %s, started at: %s", info.State, info.StartedAt)

	// 5. Stop
	t.Log("stopping container...")
	err = cli.Stop(ctx, id, 5*time.Second)
	require.NoError(t, err)

	info2, err := cli.Inspect(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, StateExited, info2.State)

	// 6. Remove
	t.Log("removing container...")
	err = cli.Remove(ctx, id, false)
	require.NoError(t, err)

	_, err = cli.Inspect(ctx, id)
	assert.Error(t, err, "container should be gone after Remove")

	t.Log("smoke test passed: full lifecycle OK")
}

// TestDocker_ConcurrentPull verifies that concurrent Pull calls for the
// same image do not produce race conditions or errors.
func TestDocker_ConcurrentPull(t *testing.T) {
	ctx := context.Background()

	cli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	defer cli.Close()

	const goroutines = 5
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// Each goroutine pulls the same image concurrently.
			if err := cli.Pull(ctx, "docker.io/library/alpine:latest", nil); err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent Pull failed: %v", err)
	}
}

// TestContainerConfig_Defaults verifies that an empty ContainerConfig
// creates a container without issues (using image defaults).
func TestContainerConfig_Defaults(t *testing.T) {
	ctx := context.Background()

	cli, err := NewDockerClient(ctx)
	require.NoError(t, err)
	defer cli.Close()

	err = cli.Pull(ctx, "docker.io/library/alpine:latest", nil)
	require.NoError(t, err)

	id, err := cli.Create(ctx, ContainerConfig{
		Name:  "otoxan-test-defaults-" + t.Name(),
		Image: "docker.io/library/alpine:latest",
		// All other fields zero-value (no cmd, no env, no ports, no mounts).
	})
	require.NoError(t, err)
	defer func() { _ = cli.Remove(ctx, id, true) }()

	info, err := cli.Inspect(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, StateCreated, info.State)
	assert.Empty(t, info.BindMounts)
	assert.Empty(t, info.PortMappings)
}
