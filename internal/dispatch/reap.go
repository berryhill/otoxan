package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const completedDir = "/tmp/otoxan_completed"

// ------------------------------------------------------------------
// Dependencies
// ------------------------------------------------------------------

// ReapDeps holds the external dependencies required by the reap loop.
type ReapDeps struct {
	// SpawnsColl is the MongoDB dispatch_spawns collection (optional).
	// If nil, PID lookup is skipped.
	SpawnsColl *mongo.Collection

	// Logger is a minimal logger interface.
	// If nil, logging is silently discarded.
	Logger interface {
		Printf(format string, v ...interface{})
	}
}

// ------------------------------------------------------------------
// Public API
// ------------------------------------------------------------------

// RunReapLoop ticks every 5 seconds, scans /tmp/otoxan_completed/*.json,
// parses each marker file into a Completion, pushes it to out, and deletes
// the file.  Malformed markers are logged and deleted without blocking the
// loop.  The loop honours ctx.Done() for clean shutdown.
func RunReapLoop(ctx context.Context, deps ReapDeps, out chan<- *Completion) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		if err := reapOnce(ctx, deps, out); err != nil {
			// Log and continue; transient filesystem errors should not kill
			// the dispatcher.
			logf(deps.Logger, "reapOnce error: %v", err)
		}
	}
}

// reapOnce performs a single scan of the marker directory.
func reapOnce(ctx context.Context, deps ReapDeps, out chan<- *Completion) error {
	entries, err := os.ReadDir(completedDir)
	if err != nil {
		if os.IsNotExist(err) {
			logf(deps.Logger, "reapOnce: marker dir does not exist")
			return nil // nothing to reap
		}
		return fmt.Errorf("reapOnce: ReadDir(%q): %w", completedDir, err)
	}

	logf(deps.Logger, "reapOnce: found %d entries in %s", len(entries), completedDir)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(completedDir, entry.Name())

		comp, err := parseMarker(path)
		if err != nil {
			logf(deps.Logger, "reapOnce: malformed marker %q: %v", path, err)
			_ = os.Remove(path)
			continue
		}

		// Best-effort: if the worker PID is still alive, kill it.
		// This handles the edge case where the worker forked but the
		// parent process exited before the marker was written.
		//
		// NOTE: PID is not part of the Completion marker format (DS-3).
		// We look it up from the spawn record instead.
		pid := lookupPID(ctx, deps, comp.TaskID)
		if pid > 0 && isPIDAlive(pid) {
			logf(deps.Logger, "reapOnce: killing still-alive PID %d for task %s", pid, comp.TaskID)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}

		select {
		case out <- comp:
			logf(deps.Logger, "reapOnce: sent completion for task %s", comp.TaskID)
			// successfully handed off
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Channel full — skip deletion so the file can be reaped next tick.
			logf(deps.Logger, "reapOnce: channel full for task %s, will retry", comp.TaskID)
			continue
		}

		_ = os.Remove(path)
	}

	return nil
}

// parseMarker reads and unmarshals a single JSON marker file.
func parseMarker(path string) (*Completion, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var comp Completion
	if err := json.Unmarshal(data, &comp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	if comp.TaskID == "" {
		return nil, fmt.Errorf("missing task_id")
	}

	return &comp, nil
}

// isPIDAlive returns true if the given PID exists and is not a zombie.
func isPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds; we must signal 0 to check liveness.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// lookupPID returns the PID from the dispatch_spawns record for the given
// task, or 0 if not found.
func lookupPID(ctx context.Context, deps ReapDeps, taskID string) int {
	if deps.SpawnsColl == nil {
		return 0
	}
	var rec SpawnRecord
	if err := deps.SpawnsColl.FindOne(ctx, bson.M{"task_id": taskID}).Decode(&rec); err != nil {
		return 0
	}
	return rec.PID
}

// logf is a best-effort printf wrapper.
func logf(logger interface{ Printf(format string, v ...interface{}) }, format string, v ...interface{}) {
	if logger != nil {
		logger.Printf(format, v...)
	}
}
