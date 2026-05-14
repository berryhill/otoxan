package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/silas/otoxan/internal/runtime/containerstore"
	"github.com/silas/otoxan/internal/runtime/types"
)

// Re-export types and constants so callers can use the runtime package without
// importing the types subpackage directly.
type (
	ContainerState = types.ContainerState
	ContainerInfo  = types.ContainerInfo
)

const (
	StateCreated    = types.StateCreated
	StateRunning    = types.StateRunning
	StatePaused     = types.StatePaused
	StateRestarting = types.StateRestarting
	StateExited     = types.StateExited
	StateDead       = types.StateDead
	StateRemoving   = types.StateRemoving
)

// SpinupConfig describes the parameters for spinning up a new container.
type SpinupConfig struct {
	// Name is an optional container name. If empty the daemon assigns a random name.
	Name string

	// Image is the image reference to pull and run.
	Image string

	// Cmd is the entrypoint command. If nil the image defaults are used.
	Cmd []string

	// Env is a list of "KEY=VALUE" environment variables.
	Env []string

	// BindMounts maps host paths to container paths.
	// Format: "hostPath:containerPath[:ro]"
	BindMounts []string

	// Ports maps host ports to container ports.
	// Format: "hostPort:containerPort/proto"
	Ports []string

	// AutoRemove controls whether Docker removes the container after exit.
	AutoRemove bool

	// Owner is the agent or system identifier that owns this container.
	Owner string

	// OwnerType is "agent" or "system".
	OwnerType string

	// Role is a semantic label: "indexer", "dispatch_worker", "companion", etc.
	Role string

	// StopTimeout is how long to wait for graceful shutdown (0 = Docker default 10s).
	StopTimeout time.Duration
}

// SpinupResult holds the result of a successful Spinup call.
type SpinupResult struct {
	ContainerID  string
	ContainerDoc *containerstore.ContainerDoc
}

// Spinup pulls an image, creates and starts a container, and registers it in
// the MongoDB containers collection.
//
// The container lifecycle (pull → create → start) is atomic in terms of
// registration: if any step fails after registration, the partial state is
// cleaned up by the watcher or by explicit teardown.
func Spinup(ctx context.Context, dockerCli *DockerClient, store *containerstore.Store, cfg SpinupConfig) (*SpinupResult, error) {
	// Apply defaults.
	ownerType := cfg.OwnerType
	if ownerType == "" {
		ownerType = "system"
	}

	containerName := cfg.Name
	if containerName == "" {
		// Generate a deterministic-ish name from owner + role.
		slug := fmt.Sprintf("%s-%s", cfg.Owner, cfg.Role)
		containerName = fmt.Sprintf("otoxan-%s-%d", sanitizeName(slug), time.Now().UnixNano()%1e6)
	}

	// 1. Pull the image.
	if err := dockerCli.Pull(ctx, cfg.Image, nil); err != nil {
		return nil, fmt.Errorf("spinup: pull %s: %w", cfg.Image, err)
	}

	// 2. Create the container (stopped state).
	dockerCfg := ContainerConfig{
		Name:       containerName,
		Image:      cfg.Image,
		Cmd:        cfg.Cmd,
		Env:        cfg.Env,
		BindMounts: cfg.BindMounts,
		Ports:      cfg.Ports,
		AutoRemove: cfg.AutoRemove,
	}

	containerID, err := dockerCli.Create(ctx, dockerCfg)
	if err != nil {
		return nil, fmt.Errorf("spinup: create %s: %w", containerName, err)
	}

	// Trim to short ID for storage.
	shortID := trimID(containerID)

	// 3. Register the container in Mongo (created state) before starting.
	now := time.Now().UTC()
	doc := &containerstore.ContainerDoc{
		ContainerID:  shortID,
		Name:         containerName,
		Image:        cfg.Image,
		Owner:        cfg.Owner,
		OwnerType:    ownerType,
		Role:         cfg.Role,
		Status:       string(StateCreated),
		CreatedAt:    now,
		UpdatedAt:    now,
		BindMounts:   cfg.BindMounts,
		PortMappings: []string{},
	}

	if err := store.Upsert(ctx, doc); err != nil {
		// Best-effort cleanup: remove the Docker container.
		_ = dockerCli.Remove(ctx, containerID, true)
		return nil, fmt.Errorf("spinup: upsert container %s: %w", shortID, err)
	}

	// 4. Start the container.
	if err := dockerCli.Start(ctx, containerID); err != nil {
		// Mark the document as failed; watcher will clean up.
		_ = store.Upsert(ctx, &containerstore.ContainerDoc{
			ContainerID: shortID,
			Name:        containerName,
			Image:       cfg.Image,
			Owner:       cfg.Owner,
			OwnerType:   ownerType,
			Role:        cfg.Role,
			Status:      string(StateExited),
			ExitCode:    1,
			UpdatedAt:   time.Now().UTC(),
		})
		return nil, fmt.Errorf("spinup: start %s: %w", containerName, err)
	}

	// 5. Refresh status from Docker and update Mongo.
	info, err := dockerCli.Inspect(ctx, containerID)
	if err != nil {
		// Non-fatal: log and return what we know.
		doc.Status = string(StateRunning)
		doc.StartedAt = now
		return &SpinupResult{ContainerID: shortID, ContainerDoc: doc}, nil
	}

	doc.Status = string(info.State)
	doc.StartedAt = info.StartedAt
	doc.ExitCode = info.ExitCode
	doc.PortMappings = info.PortMappings
	doc.UpdatedAt = time.Now().UTC()

	if err := store.Upsert(ctx, doc); err != nil {
		return nil, fmt.Errorf("spinup: update status for %s: %w", shortID, err)
	}

	return &SpinupResult{ContainerID: shortID, ContainerDoc: doc}, nil
}

// SpinupSimple is a convenience wrapper for spinning up a container with
// minimal configuration. It uses the "system" owner type and no bind mounts.
func SpinupSimple(ctx context.Context, dockerCli *DockerClient, store *containerstore.Store, name, image string, role string) (*SpinupResult, error) {
	return Spinup(ctx, dockerCli, store, SpinupConfig{
		Name:       name,
		Image:      image,
		Role:       role,
		AutoRemove: false,
	})
}

// Teardown stops and removes a container, then deletes its MongoDB document.
func Teardown(ctx context.Context, dockerCli *DockerClient, store *containerstore.Store, containerID string, force bool, timeout time.Duration) error {
	// Try to stop first. If already stopped or unknown, remove anyway.
	_ = dockerCli.Stop(ctx, containerID, timeout)
	_ = dockerCli.Remove(ctx, containerID, force)
	// Delete the Mongo document regardless of Docker state.
	if err := store.Delete(ctx, containerID); err != nil {
		return fmt.Errorf("teardown: delete %s from store: %w", containerID, err)
	}
	return nil
}

// TeardownByOwner stops and removes all containers owned by a given owner.
func TeardownByOwner(ctx context.Context, dockerCli *DockerClient, store *containerstore.Store, owner string, force bool, timeout time.Duration) ([]string, error) {
	docs, err := store.ListByOwner(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("teardown by owner: list %s: %w", owner, err)
	}

	var removed []string
	for _, doc := range docs {
		if err := Teardown(ctx, dockerCli, store, doc.ContainerID, force, timeout); err != nil {
			// Log but continue.
			continue
		}
		removed = append(removed, doc.ContainerID)
	}
	return removed, nil
}

// sanitizeName replaces characters that are invalid in Docker container names.
func sanitizeName(s string) string {
	s = strings.ToLower(s)
	replacer := strings.NewReplacer(
		"/", "-",
		"_", "-",
		" ", "-",
	)
	return replacer.Replace(s)
}

// trimID returns the short form (12-char) of a container ID.
func trimID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// ------------------------------------------------------------------
// Watcher
// ------------------------------------------------------------------

// Watcher monitors Docker containers and syncs their status to the MongoDB
// containers collection. It is designed to run as a background goroutine.
type Watcher struct {
	dockerCli *DockerClient
	store     *containerstore.Store
	interval  time.Duration
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

// NewWatcher creates a new container watcher.
func NewWatcher(dockerCli *DockerClient, store *containerstore.Store, interval time.Duration) *Watcher {
	if interval == 0 {
		interval = 10 * time.Second
	}
	return &Watcher{
		dockerCli: dockerCli,
		store:     store,
		interval:  interval,
		stopCh:    make(chan struct{}),
	}
}

// Start launches the watcher loop in a background goroutine.
func (w *Watcher) Start(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.run(ctx)
	}()
}

// Stop signals the watcher to stop and waits for it to exit.
func (w *Watcher) Stop() {
	close(w.stopCh)
	w.wg.Wait()
}

// run is the main watcher loop.
func (w *Watcher) run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.syncAll(ctx)
		}
	}
}

// syncAll fetches all containers in the store and syncs their status.
func (w *Watcher) syncAll(ctx context.Context) {
	docs, err := w.store.List(ctx, "", "") // all
	if err != nil {
		return
	}

	for _, doc := range docs {
		// Try to inspect the container in Docker.
		info, err := w.dockerCli.Inspect(ctx, doc.ContainerID)
		if err != nil {
			// Container no longer exists in Docker — remove from store.
			_ = w.store.Delete(ctx, doc.ContainerID)
			continue
		}

		// Only update if state actually changed.
		if doc.Status == string(info.State) &&
			doc.ExitCode == info.ExitCode {
			continue
		}

		if err := w.store.UpdateStatus(ctx, doc.ContainerID, info); err != nil {
			continue
		}
	}
}

// SyncOne syncs a single container by ID. Useful after explicit lifecycle operations.
func (w *Watcher) SyncOne(ctx context.Context, containerID string) error {
	info, err := w.dockerCli.Inspect(ctx, containerID)
	if err != nil {
		// Container gone — remove from store.
		return w.store.Delete(ctx, containerID)
	}
	return w.store.UpdateStatus(ctx, containerID, info)
}
