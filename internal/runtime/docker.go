package runtime

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"

	"github.com/silas/otoxan/internal/runtime/types"
)

// DockerClient wraps the official Docker SDK client and provides a slim,
// domain-focused interface for otoxan's container lifecycle operations:
// Pull, Create, Start, Inspect, Stop, and Remove.
//
// All methods accept a context.Context so that long-running operations
// (especially Pull) can be cancelled or timed out by the caller.
// The zero value of DockerClient is not ready to use; construct it with
// NewDockerClient.
type DockerClient struct {
	cli *client.Client
}

// NewDockerClient connects to the local Docker daemon via the default
// socket (on Linux: /var/run/docker.sock, on Windows: named pipe).
// It returns an error if the daemon is not reachable.
func NewDockerClient(ctx context.Context) (*DockerClient, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return nil, fmt.Errorf("runtime/docker: NewClientWithOpts: %w", err)
	}
	return &DockerClient{cli: cli}, nil
}

// Close releases resources held by the underlying Docker client.
// It should be called when the DockerClient is no longer needed.
func (dc *DockerClient) Close() error {
	if dc.cli == nil {
		return nil
	}
	return dc.cli.Close()
}

// Pull downloads the requested image tag from its registry.
// ref is the image reference, e.g. "nginx:latest" or
// "ghcr.io/silas/otoxan-indexer:v1.2.0".
//
// Pull is idempotent: if the image is already present the daemon simply
// validates the local layer chain and returns quickly.
//
// The progress writer is optional; passing nil discards pull stream output.
func (dc *DockerClient) Pull(ctx context.Context, ref string, progress io.Writer) error {
	pullCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	rc, err := dc.cli.ImagePull(pullCtx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("runtime/docker: ImagePull %q: %w", ref, err)
	}
	defer rc.Close()

	// Drain the stream, optionally writing to progress.
	if progress != nil {
		_, err = io.Copy(progress, rc)
		if err != nil {
			return fmt.Errorf("runtime/docker: reading pull stream for %q: %w", ref, err)
		}
	} else {
		// Discard but still drain to unblock the stream.
		_, err = io.Copy(io.Discard, rc)
		if err != nil {
			return fmt.Errorf("runtime/docker: draining pull stream for %q: %w", ref, err)
		}
	}

	return nil
}

// ContainerConfig describes the parameters needed to create a new container.
// It is a slimmed-down projection of the Docker API; advanced fields that
// otoxan does not use are omitted for clarity.
type ContainerConfig struct {
	// Name is an optional container name. If empty the daemon assigns a
	// random name. Explicit names make inspection and cleanup deterministic.
	Name string

	// Image is the image reference (as used in Pull) that the container
	// will run.
	Image string

	// Cmd is the entrypoint command and arguments. If nil the image's
	// default ENTRYPOINT + CMD are used.
	Cmd []string

	// Env is a list of environment variables in "KEY=VALUE" form.
	// Variables from the host environment can be forwarded by the caller.
	Env []string

	// BindMounts maps host paths to container paths.
	// Format: "hostPath:containerPath[:ro]"
	BindMounts []string

	// Ports maps host ports to container ports.
	// Format: "hostPort:containerPort/proto" (e.g. "8080:80/tcp").
	Ports []string

	// AutoRemove controls whether Docker removes the container filesystem
	// after the container exits (equivalent to `docker run --rm`).
	AutoRemove bool
}

// Create requests the Docker daemon to create a container from the given
// image and configuration. It returns the container ID on success.
//
// The container is created in the stopped state; call Start to run it.
func (dc *DockerClient) Create(ctx context.Context, cfg ContainerConfig) (string, error) {
	// Build host config.
	hc := &container.HostConfig{
		AutoRemove: cfg.AutoRemove,
	}

	if len(cfg.BindMounts) > 0 {
		hc.Binds = cfg.BindMounts
	}

	// Build port bindings and exposed ports.
	var portBindings nat.PortMap
	var exposedPorts nat.PortSet
	if len(cfg.Ports) > 0 {
		portBindings = make(nat.PortMap)
		exposedPorts = make(nat.PortSet)

		for _, p := range cfg.Ports {
			binding, port, err := parsePortBinding(p)
			if err != nil {
				return "", fmt.Errorf("runtime/docker: Create: parse port %q: %w", p, err)
			}
			portBindings[port] = append(portBindings[port], binding)
			exposedPorts[port] = struct{}{}
		}
	}

	hc.PortBindings = portBindings

	// Build container config.
	cc := &container.Config{
		Image:        cfg.Image,
		Cmd:          cfg.Cmd,
		Env:          cfg.Env,
		ExposedPorts: exposedPorts,
	}

	resp, err := dc.cli.ContainerCreate(ctx, cc, hc, &network.NetworkingConfig{}, nil, cfg.Name)
	if err != nil {
		return "", fmt.Errorf("runtime/docker: ContainerCreate: %w", err)
	}

	return resp.ID, nil
}

// Start transitions a previously-created container (by ID or name) to the
// running state. It is idempotent for already-running containers.
func (dc *DockerClient) Start(ctx context.Context, containerID string) error {
	err := dc.cli.ContainerStart(ctx, containerID, container.StartOptions{})
	if err != nil {
		return fmt.Errorf("runtime/docker: ContainerStart %q: %w", containerID, err)
	}
	return nil
}

// Inspect returns comprehensive information about a container.
// The returned types.ContainerInfo embeds the raw Docker API container JSON
// and additionally normalises the state to a typed State field.
func (dc *DockerClient) Inspect(ctx context.Context, containerID string) (*types.ContainerInfo, error) {
	raw, err := dc.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("runtime/docker: ContainerInspect %q: %w", containerID, err)
	}

	info := &types.ContainerInfo{
		ID:      raw.ID,
		Name:    raw.Name,
		Image:   raw.Config.Image,
		Created: raw.Created,
	}

	if raw.State != nil {
		info.State = types.ContainerState(raw.State.Status)
		info.ExitCode = raw.State.ExitCode

		// Parse Docker's RFC3339 timestamps.
		if raw.State.StartedAt != "" {
			t, _ := time.Parse(time.RFC3339, raw.State.StartedAt)
			info.StartedAt = t
		}
		if raw.State.FinishedAt != "" {
			t, _ := time.Parse(time.RFC3339, raw.State.FinishedAt)
			info.FinishedAt = t
		}
	}

	// Extract bind mounts.
	if raw.HostConfig != nil && len(raw.HostConfig.Binds) > 0 {
		info.BindMounts = raw.HostConfig.Binds
	}

	// Extract port mappings from NetworkSettings.
	// Docker may report the same mapping multiple times (e.g. once per network
	// or duplicated entries for the same port). Deduplicate with a map.
	if raw.NetworkSettings != nil {
		seen := make(map[string]bool)
		for port, bindings := range raw.NetworkSettings.Ports {
			for _, binding := range bindings {
				if binding.HostPort != "" {
					key := fmt.Sprintf("%s:%s", binding.HostPort, port.Port())
					if !seen[key] {
						seen[key] = true
						info.PortMappings = append(info.PortMappings, key)
					}
				}
			}
		}
	}

	return info, nil
}

// Stop sends a SIGTERM to the init process inside the container and waits
// up to timeout seconds for the container to exit gracefully. If the
// container has not stopped after timeout, a SIGKILL is sent.
//
// A zero timeout delegates to Docker's per-container StopTimeout (or its
// default of 10 seconds).
func (dc *DockerClient) Stop(ctx context.Context, containerID string, timeout time.Duration) error {
	var opts container.StopOptions
	if timeout > 0 {
		secs := int(timeout.Seconds())
		opts.Timeout = &secs
	}
	err := dc.cli.ContainerStop(ctx, containerID, opts)
	if err != nil {
		return fmt.Errorf("runtime/docker: ContainerStop %q: %w", containerID, err)
	}
	return nil
}

// Remove deletes a container and its filesystem. The container must be
// stopped (or not running) before removal; otherwise an error is returned.
//
// Passing force=true sends SIGKILL before removing (equivalent to
// `docker rm -f`). Without force, removal fails if the container is running.
func (dc *DockerClient) Remove(ctx context.Context, containerID string, force bool) error {
	err := dc.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{
		Force: force,
	})
	if err != nil {
		return fmt.Errorf("runtime/docker: ContainerRemove %q: %w", containerID, err)
	}
	return nil
}

// parsePortBinding splits a Docker-style port binding string into a
// nat.PortBinding and nat.Port.
// Accepts formats: "8080:80", "8080:80/tcp", "9090/udp".
func parsePortBinding(binding string) (nat.PortBinding, nat.Port, error) {
	// Use nat.ParsePortSpec to do the heavy lifting.
	mappings, err := nat.ParsePortSpec(binding)
	if err != nil {
		return nat.PortBinding{}, "", fmt.Errorf("invalid port binding %q: %w", binding, err)
	}
	return mappings[0].Binding, mappings[0].Port, nil
}

// parsePortBindingStrings parses a list of Docker-style port strings and
// returns a nat.PortSet and nat.PortMap suitable for container creation.
func parsePortBindingStrings(ports []string) (nat.PortSet, nat.PortMap, error) {
	exposedPorts := make(nat.PortSet)
	portBindings := make(nat.PortMap)

	for _, p := range ports {
		binding, port, err := parsePortBinding(p)
		if err != nil {
			return nil, nil, err
		}
		exposedPorts[port] = struct{}{}
		portBindings[port] = append(portBindings[port], binding)
	}

	return exposedPorts, portBindings, nil
}

// splitPortBinding is a simple parser for "hostPort:containerPort[/proto]".
// It is used internally to construct port mappings for Inspect output.
func splitPortBinding(raw string) (hostPort, containerPort, proto string) {
	// Docker format: [hostIP:]hostPort[:containerPort][/proto]
	parts := strings.Split(raw, ":")
	switch len(parts) {
	case 2:
		// hostPort:containerPort
		return parts[0], parts[1], "tcp"
	case 3:
		// hostIP:hostPort:containerPort or hostPort:containerPort/proto
		if strings.Contains(parts[2], "/") {
			pp := strings.Split(parts[2], "/")
			return parts[0], pp[0], pp[1]
		}
		return parts[0], parts[1], "tcp"
	case 4:
		// hostIP:hostPort:containerPort/proto
		pp := strings.Split(parts[3], "/")
		return parts[1], pp[0], pp[1]
	default:
		return "", raw, "tcp"
	}
}

// _ compiles the splitPortBinding function so it is included in the build
// even if unused in docker.go itself (it is kept for potential future use).
var _ = strconv.IntSize
