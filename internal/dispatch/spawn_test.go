package dispatch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ------------------------------------------------------------------
// Test helpers
// ------------------------------------------------------------------

// newSpawnTestColl returns a fresh dispatch_spawns collection.
func newSpawnTestColl(t *testing.T, client *mongo.Client) *mongo.Collection {
	t.Helper()
	return client.Database("silas").Collection(fmt.Sprintf("dispatch_spawns_%d", time.Now().UnixNano()))
}

// fakeWorkerBinary writes a tiny shell script that sleeps for a given duration
// and then exits 0.  Returns the absolute path to the script.
func fakeWorkerBinary(t *testing.T, sleepMs int) string {
	t.Helper()
	var script string
	if runtime.GOOS == "windows" {
		// Windows batch fallback — not expected in this environment.
		t.Skip("fakeWorkerBinary not implemented for Windows")
	}
	script = fmt.Sprintf("#!/bin/sh\nsleep %d\n", sleepMs)
	f, err := os.CreateTemp("", "otoxan-worker-*.sh")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(script); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := os.Chmod(f.Name(), 0755); err != nil {
		t.Fatalf("chmod temp: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	return f.Name()
}

// seedClaimed inserts a single CLAIMED dispatch_request.
func seedClaimed(ctx context.Context, coll *mongo.Collection, reqID, taskID string) error {
	doc := DispatchRequest{
		RequestID: reqID,
		TaskID:    taskID,
		Status:    RequestClaimed,
		CreatedAt: time.Now().UTC(),
	}
	_, err := coll.InsertOne(ctx, doc)
	return err
}

// ------------------------------------------------------------------
// Unit tests
// ------------------------------------------------------------------

func TestSpawnOne_Success(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	reqColl := newClaimTestColl(t, client)
	spawnColl := newSpawnTestColl(t, client)

	reqID := "dr_spawn_ok"
	taskID := "t_spawn_ok"
	if err := seedClaimed(ctx, reqColl, reqID, taskID); err != nil {
		t.Fatalf("seedClaimed failed: %v", err)
	}

	binary := fakeWorkerBinary(t, 500) // sleeps 500ms

	deps := SpawnDeps{
		SpawnsColl:   spawnColl,
		RequestsColl: reqColl,
		AgentID:      "agent-spawn",
		WorkerBinary: binary,
	}

	req := &DispatchRequest{RequestID: reqID, TaskID: taskID, Status: RequestClaimed}
	if err := spawnOne(ctx, deps, req); err != nil {
		t.Fatalf("spawnOne failed: %v", err)
	}

	// Verify request is marked RUNNING (FULFILLED in the enum).
	var raw bson.M
	if err := reqColl.FindOne(ctx, bson.M{"request_id": reqID}).Decode(&raw); err != nil {
		t.Fatalf("request lookup failed: %v", err)
	}
	if raw["status"] != string(RequestFulfilled) {
		t.Errorf("expected status %q, got %v", RequestFulfilled, raw["status"])
	}
	if raw["started_by"] != "agent-spawn" {
		t.Errorf("expected started_by %q, got %v", "agent-spawn", raw["started_by"])
	}

	// Verify spawn record exists with RUNNING status.
	var rec SpawnRecord
	if err := spawnColl.FindOne(ctx, bson.M{"task_id": taskID}).Decode(&rec); err != nil {
		t.Fatalf("spawn lookup failed: %v", err)
	}
	if rec.Status != SpawnRunning {
		t.Errorf("expected spawn status %q, got %q", SpawnRunning, rec.Status)
	}
	if rec.PID == 0 {
		t.Error("expected non-zero PID")
	}
	if rec.RequestID != reqID {
		t.Errorf("expected request_id %q, got %q", reqID, rec.RequestID)
	}

	// Give the fake worker time to finish so we don't leave zombies.
	time.Sleep(600 * time.Millisecond)
}

func TestSpawnOne_BinaryNotFound_Requeues(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	reqColl := newClaimTestColl(t, client)
	spawnColl := newSpawnTestColl(t, client)

	reqID := "dr_spawn_nobin"
	taskID := "t_spawn_nobin"
	if err := seedClaimed(ctx, reqColl, reqID, taskID); err != nil {
		t.Fatalf("seedClaimed failed: %v", err)
	}

	deps := SpawnDeps{
		SpawnsColl:   spawnColl,
		RequestsColl: reqColl,
		AgentID:      "agent-spawn",
		WorkerBinary: "this-binary-definitely-does-not-exist-otoxan-worker",
	}

	req := &DispatchRequest{RequestID: reqID, TaskID: taskID, Status: RequestClaimed}
	err := spawnOne(ctx, deps, req)
	if err == nil {
		t.Fatal("expected error when binary missing")
	}

	// Verify request was reset to PENDING.
	var raw bson.M
	if err := reqColl.FindOne(ctx, bson.M{"request_id": reqID}).Decode(&raw); err != nil {
		t.Fatalf("request lookup failed: %v", err)
	}
	if raw["status"] != string(RequestPending) {
		t.Errorf("expected status %q after re-queue, got %v", RequestPending, raw["status"])
	}
	if _, ok := raw["claimed_by"]; ok {
		t.Error("claimed_by should be unset after re-queue")
	}

	// Verify no spawn record was created.
	count, err := spawnColl.CountDocuments(ctx, bson.M{"task_id": taskID})
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 spawn records, got %d", count)
	}
}

func TestSpawnOne_MarkRunningFails(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	reqColl := newClaimTestColl(t, client)
	spawnColl := newSpawnTestColl(t, client)

	// Do NOT seed the request — markRequestRunning should fail because there
	// is no matching CLAIMED document.
	binary := fakeWorkerBinary(t, 500)

	deps := SpawnDeps{
		SpawnsColl:   spawnColl,
		RequestsColl: reqColl,
		AgentID:      "agent-spawn",
		WorkerBinary: binary,
	}

	req := &DispatchRequest{RequestID: "dr_missing", TaskID: "t_missing", Status: RequestClaimed}
	err := spawnOne(ctx, deps, req)
	if err == nil {
		t.Fatal("expected error when request not found")
	}

	// Verify no spawn record was created.
	count, err := spawnColl.CountDocuments(ctx, bson.M{"task_id": "t_missing"})
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 spawn records, got %d", count)
	}
}

func TestSpawnOne_CommandRunnerOverride(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	reqColl := newClaimTestColl(t, client)
	spawnColl := newSpawnTestColl(t, client)

	reqID := "dr_runner"
	taskID := "t_runner"
	if err := seedClaimed(ctx, reqColl, reqID, taskID); err != nil {
		t.Fatalf("seedClaimed failed: %v", err)
	}

	// Use a custom CommandRunner that invokes /bin/sh -c "exit 0".
	// We still verify the PID is recorded and the process starts.
	runnerCalled := false
	deps := SpawnDeps{
		SpawnsColl:   spawnColl,
		RequestsColl: reqColl,
		AgentID:      "agent-runner",
		WorkerBinary: "fake-binary-ignored",
		CommandRunner: func(ctx context.Context, name string, arg ...string) *exec.Cmd {
			runnerCalled = true
			// Ignore the passed name and just run a short sleep.
			return exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 0.2")
		},
	}

	req := &DispatchRequest{RequestID: reqID, TaskID: taskID, Status: RequestClaimed}
	if err := spawnOne(ctx, deps, req); err != nil {
		t.Fatalf("spawnOne failed: %v", err)
	}
	if !runnerCalled {
		t.Fatal("expected CommandRunner to be called")
	}

	var rec SpawnRecord
	if err := spawnColl.FindOne(ctx, bson.M{"task_id": taskID}).Decode(&rec); err != nil {
		t.Fatalf("spawn lookup failed: %v", err)
	}
	if rec.Status != SpawnRunning {
		t.Errorf("expected spawn status %q, got %q", SpawnRunning, rec.Status)
	}
	if rec.PID == 0 {
		t.Error("expected non-zero PID")
	}

	time.Sleep(300 * time.Millisecond)
}

func TestRunSpawnSupervisor_Basic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := setupMongo(t)
	reqColl := newClaimTestColl(t, client)
	spawnColl := newSpawnTestColl(t, client)

	// Seed 3 CLAIMED requests.
	for i := 0; i < 3; i++ {
		reqID := fmt.Sprintf("dr_sup_%d", i)
		taskID := fmt.Sprintf("t_sup_%d", i)
		if err := seedClaimed(ctx, reqColl, reqID, taskID); err != nil {
			t.Fatalf("seedClaimed failed: %v", err)
		}
	}

	binary := fakeWorkerBinary(t, 300)

	in := make(chan *DispatchRequest, 3)
	for i := 0; i < 3; i++ {
		in <- &DispatchRequest{
			RequestID: fmt.Sprintf("dr_sup_%d", i),
			TaskID:    fmt.Sprintf("t_sup_%d", i),
			Status:    RequestClaimed,
		}
	}
	close(in)

	deps := SpawnDeps{
		SpawnsColl:   spawnColl,
		RequestsColl: reqColl,
		AgentID:      "agent-supervisor",
		WorkerBinary: binary,
	}

	if err := RunSpawnSupervisor(ctx, deps, in); err != nil {
		t.Fatalf("RunSpawnSupervisor returned unexpected error: %v", err)
	}

	// All three should have spawn records.
	count, err := spawnColl.CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 spawn records, got %d", count)
	}

	// All three requests should be RUNNING.
	count, err = reqColl.CountDocuments(ctx, bson.M{"status": RequestFulfilled})
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 RUNNING requests, got %d", count)
	}

	time.Sleep(400 * time.Millisecond)
}

func TestRunSpawnSupervisor_CleanShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	client := setupMongo(t)
	reqColl := newClaimTestColl(t, client)
	spawnColl := newSpawnTestColl(t, client)

	binary := fakeWorkerBinary(t, 5000) // sleeps 5s

	in := make(chan *DispatchRequest)
	deps := SpawnDeps{
		SpawnsColl:   spawnColl,
		RequestsColl: reqColl,
		AgentID:      "agent-shutdown",
		WorkerBinary: binary,
	}

	done := make(chan error, 1)
	go func() {
		done <- RunSpawnSupervisor(ctx, deps, in)
	}()

	// Give the goroutine a moment to start blocking on the channel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunSpawnSupervisor did not exit after cancel")
	}
}

// ------------------------------------------------------------------
// Umbrella runner
// ------------------------------------------------------------------

func TestSpawn(t *testing.T) {
	t.Run("Success", func(t *testing.T) { TestSpawnOne_Success(t) })
	t.Run("BinaryNotFound_Requeues", func(t *testing.T) { TestSpawnOne_BinaryNotFound_Requeues(t) })
	t.Run("MarkRunningFails", func(t *testing.T) { TestSpawnOne_MarkRunningFails(t) })
	t.Run("CommandRunnerOverride", func(t *testing.T) { TestSpawnOne_CommandRunnerOverride(t) })
	t.Run("SupervisorBasic", func(t *testing.T) { TestRunSpawnSupervisor_Basic(t) })
	t.Run("SupervisorCleanShutdown", func(t *testing.T) { TestRunSpawnSupervisor_CleanShutdown(t) })
}
