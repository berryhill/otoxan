// Package types defines shared domain types used across the runtime package.
// This package exists to avoid import cycles: types needed by both the
// container store and the Docker client are defined here rather than
// in either one.
package types

import "time"

// ContainerState describes the normalised running state of a container.
type ContainerState string

const (
	StateCreated    ContainerState = "created"
	StateRunning    ContainerState = "running"
	StatePaused     ContainerState = "paused"
	StateRestarting ContainerState = "restarting"
	StateExited     ContainerState = "exited"
	StateDead       ContainerState = "dead"
	StateRemoving   ContainerState = "removing"
)

// ContainerInfo is the domain representation of a container's runtime state.
// It is returned by Inspect and can be stored in Mongo as part of the
// otoxan_global.containers document.
type ContainerInfo struct {
	ID           string          // Docker container ID (short or full, as returned).
	Name         string          // Human-readable name (includes leading slash from Docker).
	Image        string          // Image reference used to create this container.
	Created      string          // ISO-8601 creation timestamp from Docker.
	State        ContainerState // Normalised state: running, exited, etc.
	ExitCode     int            // Exit code when in exited state.
	StartedAt    time.Time      // Wall-clock time the container last started.
	FinishedAt   time.Time      // Wall-clock time the container last stopped.
	BindMounts   []string       // Active bind mounts as "hostPath:containerPath[:ro]" strings.
	PortMappings []string       // Active port mappings as "hostPort:containerPort/proto" strings.
	RestartCount int            // Number of times the container has been restarted.
}
