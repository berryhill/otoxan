package dispatch

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ------------------------------------------------------------------
// Dependencies
// ------------------------------------------------------------------

// SpawnDeps holds the external dependencies required by the spawn supervisor.
type SpawnDeps struct {
	// SpawnsColl is the MongoDB dispatch_spawns collection.
	SpawnsColl *mongo.Collection

	// RequestsColl is the MongoDB dispatch_requests collection (to mark RUNNING).
	RequestsColl *mongo.Collection

	// AgentID is the unique identifier of this dispatcher instance.
	AgentID string

	// WorkerBinary is the path or name of the otoxan-worker binary.
	// If empty, defaults to "otoxan-worker" (resolved via exec.LookPath).
	WorkerBinary string

	// CommandRunner allows overriding exec.CommandContext for testing.
	// If nil, the real exec.CommandContext is used.
	CommandRunner func(ctx context.Context, name string, arg ...string) *exec.Cmd
}

// ------------------------------------------------------------------
// Public API
// ------------------------------------------------------------------

// RunSpawnSupervisor receives DispatchRequests from the in channel and forks
// an otoxan-worker process for each one.  It records the PID in the
// dispatch_spawns collection, marks the request RUNNING, and detaches (does
// not Wait).  Completion is detected later via marker files by reapLoop.
//
// The loop honours ctx.Done() for clean shutdown.
func RunSpawnSupervisor(ctx context.Context, deps SpawnDeps, in <-chan *DispatchRequest) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case req, ok := <-in:
			if !ok {
				return nil
			}
			if err := spawnOne(ctx, deps, req); err != nil {
				// Log and continue; do not kill the supervisor.
				// The request may be re-queued by cleanupWorker depending on
				// the error type.
				continue
			}
		}
	}
}

// ------------------------------------------------------------------
// Internal helpers
// ------------------------------------------------------------------

// spawnOne forks a single otoxan-worker for the given request.
//
// Algorithm:
//  1. Resolve the worker binary path (exec.LookPath).
//  2. Build exec.CommandContext with --task-id and other flags.
//  3. Start the process (do not Wait).
//  4. Atomically update dispatch_requests status → RUNNING.
//  5. Insert a dispatch_spawns record with PID and RUNNING status.
//  6. Return immediately (detached).
func spawnOne(ctx context.Context, deps SpawnDeps, req *DispatchRequest) error {
	var cmd *exec.Cmd

	if deps.CommandRunner != nil {
		// Test override — skip LookPath and use the injected runner.
		cmd = deps.CommandRunner(ctx, deps.WorkerBinary,
			"--task-id", req.TaskID,
			"--request-id", req.RequestID,
			"--agent-id", deps.AgentID,
		)
	} else {
		binary := deps.WorkerBinary
		if binary == "" {
			binary = "otoxan-worker"
		}

		path, err := exec.LookPath(binary)
		if err != nil {
			// Binary not found — log and re-queue by resetting request to PENDING.
			if resetErr := resetRequestPending(ctx, deps.RequestsColl, req.RequestID); resetErr != nil {
				return fmt.Errorf("spawnOne: LookPath(%q) failed and reset also failed: %v / %w", binary, resetErr, err)
			}
			return fmt.Errorf("spawnOne: LookPath(%q) failed, request re-queued: %w", binary, err)
		}

		// Build the command.  We pass the minimal set of flags the worker needs.
		// Additional metadata (prompt, toolsets, lane) is communicated via
		// MCP-over-stdio after the worker starts.
		cmd = deps.command(ctx, path,
			"--task-id", req.TaskID,
			"--request-id", req.RequestID,
			"--agent-id", deps.AgentID,
			"--marker-dir", completedDir,
			"--prompt", " ", // dummy prompt so worker does not block on stdin
		)
	}

	// Start the process but do not Wait — the completion watcher handles
	// lifecycle via marker files.
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawnOne: Start failed for task %s: %w", req.TaskID, err)
	}

	pid := cmd.Process.Pid
	now := time.Now().UTC()

	// Mark request RUNNING in the requests collection.
	if err := markRequestRunning(ctx, deps.RequestsColl, req.RequestID, deps.AgentID, now); err != nil {
		// Best-effort kill the orphan process so we don't leak it.
		_ = cmd.Process.Kill()
		return fmt.Errorf("spawnOne: markRequestRunning failed for task %s: %w", req.TaskID, err)
	}

	// Record the spawn in the durable registry.
	record := SpawnRecord{
		TaskID:     req.TaskID,
		RequestID:  req.RequestID,
		PID:        pid,
		StartedAt:  now,
		Status:     SpawnRunning,
		Lane:       "hermes", // default lane; overridden via MCP if needed
	}
	if _, err := deps.SpawnsColl.InsertOne(ctx, record); err != nil {
		// Best-effort kill the orphan process.
		_ = cmd.Process.Kill()
		return fmt.Errorf("spawnOne: InsertOne dispatch_spawns failed for task %s: %w", req.TaskID, err)
	}

	// Detach: start a goroutine to wait for the process so we don't
	// leave zombie entries in the process table.
	go func() {
		_ = cmd.Wait()
	}()

	return nil
}

// command returns an *exec.Cmd, using the testable CommandRunner if set.
func (deps SpawnDeps) command(ctx context.Context, name string, arg ...string) *exec.Cmd {
	if deps.CommandRunner != nil {
		return deps.CommandRunner(ctx, name, arg...)
	}
	return exec.CommandContext(ctx, name, arg...)
}

// markRequestRunning atomically transitions a dispatch_request from CLAIMED
// to RUNNING.
func markRequestRunning(ctx context.Context, coll *mongo.Collection, requestID, agentID string, now time.Time) error {
	filter := bson.M{
		"request_id": requestID,
		"status":     RequestClaimed,
	}
	update := bson.M{
		"$set": bson.M{
			"status":     RequestFulfilled, // RUNNING is modelled as FULFILLED in dispatch_requests
			"updated_at": now,
			"started_at": now,
			"started_by": agentID,
		},
	}
	res, err := coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("markRequestRunning: no matching CLAIMED request for %s", requestID)
	}
	return nil
}

// resetRequestPending resets a dispatch_request back to PENDING so it can
// be re-claimed and re-spawned later (e.g. when the worker binary is missing).
func resetRequestPending(ctx context.Context, coll *mongo.Collection, requestID string) error {
	filter := bson.M{"request_id": requestID}
	update := bson.M{
		"$set": bson.M{
			"status":     RequestPending,
			"updated_at": time.Now().UTC(),
		},
		"$unset": bson.M{
			"claimed_by": "",
			"claimed_at": "",
			"started_at": "",
			"started_by": "",
		},
	}
	_, err := coll.UpdateOne(ctx, filter, update)
	return err
}
