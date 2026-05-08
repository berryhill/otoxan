package dispatch

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ------------------------------------------------------------------
// Test helpers
// ------------------------------------------------------------------

func newCompleteTestColl(t *testing.T, client *mongo.Client) *mongo.Collection {
	t.Helper()
	return client.Database("silas").Collection(fmt.Sprintf("dispatch_complete_%d", time.Now().UnixNano()))
}

// ------------------------------------------------------------------
// Unit tests
// ------------------------------------------------------------------

func TestHandleCompletion_ExitCodeZero(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	spawnColl := newCompleteTestColl(t, client)
	reqColl := newClaimTestColl(t, client)

	taskID := "t_complete_ok"
	reqID := "dr_complete_ok"
	if err := seedSpawnRunning(ctx, spawnColl, taskID, reqID, 1234); err != nil {
		t.Fatalf("seedSpawnRunning failed: %v", err)
	}
	if err := seedRequestClaimed(ctx, reqColl, taskID, reqID); err != nil {
		t.Fatalf("seedRequestClaimed failed: %v", err)
	}

	comp := &Completion{
		TaskID:         taskID,
		TaskStatus:     "COMPLETED",
		ExitCode:       0,
		RuntimeSeconds: 120,
		LastLogLines:   []string{"done"},
		CompletedAt:    time.Now().UTC(),
	}

	deps := CompletionDeps{
		SpawnsColl:   spawnColl,
		RequestsColl: reqColl,
	}

	if err := handleCompletion(ctx, deps, comp); err != nil {
		t.Fatalf("handleCompletion failed: %v", err)
	}

	// Verify spawn record updated.
	var rec SpawnRecord
	if err := spawnColl.FindOne(ctx, bson.M{"task_id": taskID}).Decode(&rec); err != nil {
		t.Fatalf("spawn lookup failed: %v", err)
	}
	if rec.Status != SpawnCompleted {
		t.Errorf("expected spawn status %q, got %q", SpawnCompleted, rec.Status)
	}
	if rec.ExitCode != 0 {
		t.Errorf("expected exit_code 0, got %d", rec.ExitCode)
	}
	if rec.RuntimeSeconds != 120 {
		t.Errorf("expected runtime_seconds 120, got %d", rec.RuntimeSeconds)
	}

	// Verify request updated.
	var raw bson.M
	if err := reqColl.FindOne(ctx, bson.M{"task_id": taskID}).Decode(&raw); err != nil {
		t.Fatalf("request lookup failed: %v", err)
	}
	if raw["status"] != string(RequestFulfilled) {
		t.Errorf("expected request status %q, got %v", RequestFulfilled, raw["status"])
	}
}

func TestHandleCompletion_NonZeroExitCode(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	spawnColl := newCompleteTestColl(t, client)
	reqColl := newClaimTestColl(t, client)

	taskID := "t_complete_fail"
	reqID := "dr_complete_fail"
	if err := seedSpawnRunning(ctx, spawnColl, taskID, reqID, 5678); err != nil {
		t.Fatalf("seedSpawnRunning failed: %v", err)
	}
	if err := seedRequestClaimed(ctx, reqColl, taskID, reqID); err != nil {
		t.Fatalf("seedRequestClaimed failed: %v", err)
	}

	comp := &Completion{
		TaskID:       taskID,
		TaskStatus:   "FAILED",
		ExitCode:     1,
		ErrorSummary: "worker crashed",
		CompletedAt:  time.Now().UTC(),
	}

	deps := CompletionDeps{
		SpawnsColl:   spawnColl,
		RequestsColl: reqColl,
	}

	if err := handleCompletion(ctx, deps, comp); err != nil {
		t.Fatalf("handleCompletion failed: %v", err)
	}

	var rec SpawnRecord
	if err := spawnColl.FindOne(ctx, bson.M{"task_id": taskID}).Decode(&rec); err != nil {
		t.Fatalf("spawn lookup failed: %v", err)
	}
	if rec.Status != SpawnFailed {
		t.Errorf("expected spawn status %q, got %q", SpawnFailed, rec.Status)
	}
	if rec.ExitCode != 1 {
		t.Errorf("expected exit_code 1, got %d", rec.ExitCode)
	}
	if rec.ErrorSummary != "worker crashed" {
		t.Errorf("expected error_summary %q, got %q", "worker crashed", rec.ErrorSummary)
	}

	var raw bson.M
	if err := reqColl.FindOne(ctx, bson.M{"task_id": taskID}).Decode(&raw); err != nil {
		t.Fatalf("request lookup failed: %v", err)
	}
	if raw["status"] != string(RequestFailed) {
		t.Errorf("expected request status %q, got %v", RequestFailed, raw["status"])
	}
}

func TestHandleCompletion_UnknownTask(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	spawnColl := newCompleteTestColl(t, client)
	reqColl := newClaimTestColl(t, client)

	comp := &Completion{
		TaskID:      "t_unknown",
		ExitCode:    0,
		CompletedAt: time.Now().UTC(),
	}

	deps := CompletionDeps{
		SpawnsColl:   spawnColl,
		RequestsColl: reqColl,
	}

	// Should not error; it logs a warning about no matching request.
	if err := handleCompletion(ctx, deps, comp); err != nil {
		t.Fatalf("handleCompletion failed: %v", err)
	}

	// No spawn record should exist.
	count, err := spawnColl.CountDocuments(ctx, bson.M{"task_id": "t_unknown"})
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 spawn records, got %d", count)
	}
}

func TestRunCompletionWatcher_CleanShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	in := make(chan *Completion)
	deps := CompletionDeps{}

	done := make(chan error, 1)
	go func() {
		done <- RunCompletionWatcher(ctx, deps, in)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunCompletionWatcher did not exit after cancel")
	}
}

func TestRunCompletionWatcher_ChannelClose(t *testing.T) {
	ctx := context.Background()

	in := make(chan *Completion)
	deps := CompletionDeps{}

	done := make(chan error, 1)
	go func() {
		done <- RunCompletionWatcher(ctx, deps, in)
	}()

	close(in)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil error on channel close, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunCompletionWatcher did not exit after channel close")
	}
}

func TestRunCompletionWatcher_ProcessesCompletions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := setupMongo(t)
	spawnColl := newCompleteTestColl(t, client)
	reqColl := newClaimTestColl(t, client)

	for i := 0; i < 3; i++ {
		taskID := fmt.Sprintf("t_watcher_%d", i)
		reqID := fmt.Sprintf("dr_watcher_%d", i)
		if err := seedSpawnRunning(ctx, spawnColl, taskID, reqID, 1000+i); err != nil {
			t.Fatalf("seedSpawnRunning failed: %v", err)
		}
		if err := seedRequestClaimed(ctx, reqColl, taskID, reqID); err != nil {
			t.Fatalf("seedRequestClaimed failed: %v", err)
		}
	}

	in := make(chan *Completion, 3)
	for i := 0; i < 3; i++ {
		in <- &Completion{
			TaskID:      fmt.Sprintf("t_watcher_%d", i),
			ExitCode:    i % 2, // 0, 1, 0
			CompletedAt: time.Now().UTC(),
		}
	}
	close(in)

	deps := CompletionDeps{
		SpawnsColl:   spawnColl,
		RequestsColl: reqColl,
	}

	if err := RunCompletionWatcher(ctx, deps, in); err != nil {
		t.Fatalf("RunCompletionWatcher failed: %v", err)
	}

	// Verify all spawns updated.
	for i := 0; i < 3; i++ {
		taskID := fmt.Sprintf("t_watcher_%d", i)
		var rec SpawnRecord
		if err := spawnColl.FindOne(ctx, bson.M{"task_id": taskID}).Decode(&rec); err != nil {
			t.Fatalf("spawn lookup failed for %s: %v", taskID, err)
		}
		wantStatus := SpawnCompleted
		if i%2 == 1 {
			wantStatus = SpawnFailed
		}
		if rec.Status != wantStatus {
			t.Errorf("task %s: expected status %q, got %q", taskID, wantStatus, rec.Status)
		}
	}
}

// ------------------------------------------------------------------
// Umbrella runner
// ------------------------------------------------------------------

func TestComplete(t *testing.T) {
	t.Run("ExitCodeZero", func(t *testing.T) { TestHandleCompletion_ExitCodeZero(t) })
	t.Run("NonZeroExitCode", func(t *testing.T) { TestHandleCompletion_NonZeroExitCode(t) })
	t.Run("UnknownTask", func(t *testing.T) { TestHandleCompletion_UnknownTask(t) })
	t.Run("CleanShutdown", func(t *testing.T) { TestRunCompletionWatcher_CleanShutdown(t) })
	t.Run("ChannelClose", func(t *testing.T) { TestRunCompletionWatcher_ChannelClose(t) })
	t.Run("ProcessesCompletions", func(t *testing.T) { TestRunCompletionWatcher_ProcessesCompletions(t) })
}
