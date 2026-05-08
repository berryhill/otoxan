package dispatch

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ------------------------------------------------------------------
// Dependencies
// ------------------------------------------------------------------

// CompletionDeps holds the external dependencies required by the
// completion watcher.
type CompletionDeps struct {
	// SpawnsColl is the MongoDB dispatch_spawns collection.
	SpawnsColl *mongo.Collection

	// RequestsColl is the MongoDB dispatch_requests collection.
	RequestsColl *mongo.Collection

	// Logger is a minimal logger interface.
	Logger interface {
		Printf(format string, v ...interface{})
	}
}

// ------------------------------------------------------------------
// Public API
// ------------------------------------------------------------------

// RunCompletionWatcher receives Completion values from the in channel,
// looks up the corresponding task/spawn record, transitions the request
// queue status (COMPLETED on exit_code 0, otherwise FAILED), and updates
// the dispatch_spawns record with the final status.  The loop honours
// ctx.Done() for clean shutdown.
func RunCompletionWatcher(ctx context.Context, deps CompletionDeps, in <-chan *Completion) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case comp, ok := <-in:
			if !ok {
				return nil
			}
			if err := handleCompletion(ctx, deps, comp); err != nil {
				logf(deps.Logger, "completion watcher: task %s: %v", comp.TaskID, err)
				// Continue; do not kill the watcher on a single bad completion.
			}
		}
	}
}

// ------------------------------------------------------------------
// Internal helpers
// ------------------------------------------------------------------

func handleCompletion(ctx context.Context, deps CompletionDeps, comp *Completion) error {
	now := time.Now().UTC()
	logf(deps.Logger, "handleCompletion: task=%s exit_code=%d", comp.TaskID, comp.ExitCode)

	// Determine final spawn status from exit code.
	var finalStatus SpawnStatus
	var requestStatus RequestStatus
	if comp.ExitCode == 0 {
		finalStatus = SpawnCompleted
		requestStatus = RequestFulfilled // COMPLETED maps to FULFILLED in dispatch_requests
	} else {
		finalStatus = SpawnFailed
		requestStatus = RequestFailed
	}

	// Update the dispatch_spawns record.
	spawnFilter := bson.M{"task_id": comp.TaskID}
	spawnUpdate := bson.M{
		"$set": bson.M{
			"status":          finalStatus,
			"exit_code":       comp.ExitCode,
			"task_status":       comp.TaskStatus,
			"runtime_seconds": comp.RuntimeSeconds,
			"error_summary":   comp.ErrorSummary,
			"log_tail":        comp.LastLogLines,
			"updated_at":      now,
		},
	}
	if _, err := deps.SpawnsColl.UpdateOne(ctx, spawnFilter, spawnUpdate); err != nil {
		return fmt.Errorf("update spawns: %w", err)
	}

	// Update the dispatch_requests record.
	reqFilter := bson.M{"task_id": comp.TaskID}
	reqUpdate := bson.M{
		"$set": bson.M{
			"status":       requestStatus,
			"updated_at":   now,
			"fulfilled_at": now,
		},
		"$setOnInsert": bson.M{
			"error": comp.ErrorSummary,
		},
	}
	res, err := deps.RequestsColl.UpdateOne(ctx, reqFilter, reqUpdate)
	if err != nil {
		return fmt.Errorf("update requests: %w", err)
	}
	if res.MatchedCount == 0 {
		logf(deps.Logger, "completion watcher: no matching request for task %s", comp.TaskID)
	}

	return nil
}
