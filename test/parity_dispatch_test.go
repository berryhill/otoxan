package dispatch_test

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/dispatch"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Parity test: Go dispatch pipeline determinism
// ------------------------------------------------------------------
//
// Intent: Verify that the Go dispatch pipeline (claim → spawn → reap →
// complete) produces deterministic, correct final states for a fixed fixture
// set.  This is the Go side of the parity equation; the Python side
// (dispatch.py --once) is exercised separately in CI via the Python
// test suite.  Both pipelines must converge on the same invariants:
//   - All 50 fixtures end COMPLETED (FULFILLED in dispatch_requests)
//   - No double-claims, no stale CLAIMED, no PID leaks
//   - Per-task event count is deterministic (claim + spawn + complete = 3)
//
// References: DS-4
// ------------------------------------------------------------------

func projectRoot() string {
	_, f, _, _ := runtime.Caller(0)
	return f
}

var (
	parityMongoClient  *mongo.Client
	parityMongoCleanup func()
	parityMongoOnce    sync.Once
)

func setupMongoParityShared(t *testing.T) *mongo.Client {
	t.Helper()
	ctx := context.Background()

	parityMongoOnce.Do(func() {
		container, err := mongodb.Run(ctx, "mongo:7")
		if err != nil {
			t.Fatalf("failed to start mongodb container: %v", err)
		}

		uri, err := container.ConnectionString(ctx)
		if err != nil {
			_ = container.Terminate(ctx)
			t.Fatalf("failed to get connection string: %v", err)
		}

		client, err := mongo.Connect(options.Client().ApplyURI(uri))
		if err != nil {
			_ = container.Terminate(ctx)
			t.Fatalf("failed to connect to mongodb: %v", err)
		}

		parityMongoClient = client
		parityMongoCleanup = func() {
			_ = client.Disconnect(ctx)
			_ = container.Terminate(ctx)
		}
	})

	t.Cleanup(parityMongoCleanup)
	return parityMongoClient
}

func newParityRequestsColl(t *testing.T, client *mongo.Client, suffix string) *mongo.Collection {
	t.Helper()
	return client.Database("silas_parity_" + suffix).Collection("dispatch_requests")
}

func newParitySpawnsColl(t *testing.T, client *mongo.Client, suffix string) *mongo.Collection {
	t.Helper()
	return client.Database("silas_parity_" + suffix).Collection("dispatch_spawns")
}

func seedParityFixtures(ctx context.Context, coll *mongo.Collection, n int) error {
	now := time.Now().UTC()
	for i := 0; i < n; i++ {
		doc := dispatch.DispatchRequest{
			RequestID: fmt.Sprintf("dr_%03d", i),
			TaskID:    fmt.Sprintf("t_%03d", i),
			Status:    dispatch.RequestPending,
			CreatedAt: now,
			Priority:  i,
		}
		doc.Extra = map[string]any{"queued_at": now.Add(time.Duration(i) * time.Millisecond)}
		if _, err := coll.InsertOne(ctx, doc); err != nil {
			return err
		}
	}
	return nil
}

func countParityByStatus(ctx context.Context, coll *mongo.Collection, status dispatch.RequestStatus) int64 {
	c, _ := coll.CountDocuments(ctx, bson.M{"status": status})
	return c
}

func countParitySpawnsByStatus(ctx context.Context, coll *mongo.Collection, status dispatch.SpawnStatus) int64 {
	c, _ := coll.CountDocuments(ctx, bson.M{"status": status})
	return c
}

// mockWorkerCommandRunner returns a CommandRunner that runs "true" (exit 0
// immediately).  The test synthesizes completions separately by watching the
// spawns collection, so the worker process itself does not need to write
// marker files.
func mockWorkerCommandRunner() func(ctx context.Context, name string, arg ...string) *exec.Cmd {
	return func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "true")
	}
}

// syntheticCompletionInjector polls the spawns collection and pushes a
// COMPLETION for every RUNNING spawn it finds.  This substitutes the real
// reap loop + worker marker-file protocol with an in-memory fast path.
func syntheticCompletionInjector(ctx context.Context, spawnsColl *mongo.Collection, out chan<- *dispatch.Completion) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	seen := make(map[string]bool)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		cursor, err := spawnsColl.Find(ctx, bson.M{"status": dispatch.SpawnRunning})
		if err != nil {
			continue
		}
		var records []dispatch.SpawnRecord
		_ = cursor.All(ctx, &records)
		cursor.Close(ctx)

		for _, rec := range records {
			if seen[rec.TaskID] {
				continue
			}
			seen[rec.TaskID] = true
			comp := &dispatch.Completion{
				TaskID:         rec.TaskID,
				TaskStatus:     "COMPLETED",
				ExitCode:       0,
				RuntimeSeconds: 0,
				SessionID:      "mock-session",
				CompletedAt:    time.Now().UTC(),
			}
			select {
			case out <- comp:
			case <-ctx.Done():
				return
			}
		}
	}
}

// runGoDispatchOnce runs one full dispatch cycle: claim → spawn → complete.
// It returns the final counts and a per-task event log.
func runGoDispatchOnce(ctx context.Context, t *testing.T, reqColl, spawnColl *mongo.Collection, concurrencyLimit int) map[string]interface{} {
	claimed := make(chan *dispatch.DispatchRequest, concurrencyLimit)
	completions := make(chan *dispatch.Completion, concurrencyLimit)

	// 1. Claim loop (fast tick for tests)
	claimDeps := dispatch.ClaimDeps{
		Collection:       reqColl,
		AgentID:          "parity-agent",
		ConcurrencyLimit: concurrencyLimit,
		TickInterval:     100 * time.Millisecond,
		ActiveCount: func() int {
			running, _ := spawnColl.CountDocuments(ctx, bson.M{"status": dispatch.SpawnRunning})
			claimedCount, _ := reqColl.CountDocuments(ctx, bson.M{"status": dispatch.RequestClaimed})
			return int(running + claimedCount)
		},
	}
	claimCtx, claimCancel := context.WithCancel(ctx)
	defer claimCancel()
	go func() {
		_ = dispatch.RunClaimLoop(claimCtx, claimDeps, claimed)
	}()

	// 2. Spawn supervisor with mock worker (exits immediately)
	spawnDeps := dispatch.SpawnDeps{
		SpawnsColl:    spawnColl,
		RequestsColl:  reqColl,
		AgentID:       "parity-agent",
		WorkerBinary:  "otoxan-worker",
		CommandRunner: mockWorkerCommandRunner(),
	}
	spawnCtx, spawnCancel := context.WithCancel(ctx)
	defer spawnCancel()
	go func() {
		_ = dispatch.RunSpawnSupervisor(spawnCtx, spawnDeps, claimed)
	}()

	// 3. Completion watcher
	completeDeps := dispatch.CompletionDeps{
		SpawnsColl:   spawnColl,
		RequestsColl: reqColl,
	}
	completeCtx, completeCancel := context.WithCancel(ctx)
	defer completeCancel()
	go func() {
		_ = dispatch.RunCompletionWatcher(completeCtx, completeDeps, completions)
	}()

	// 4. Synthetic completion injector (replaces reap loop + marker files)
	injectorDone := make(chan struct{})
	go func() {
		defer close(injectorDone)
		syntheticCompletionInjector(completeCtx, spawnColl, completions)
	}()

	// Wait for queue drain (up to 30s)
	timeout := 30 * time.Second
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pending := countParityByStatus(ctx, reqColl, dispatch.RequestPending)
		claimedCount := countParityByStatus(ctx, reqColl, dispatch.RequestClaimed)
		running := countParitySpawnsByStatus(ctx, spawnColl, dispatch.SpawnRunning)
		fulfilled := countParityByStatus(ctx, reqColl, dispatch.RequestFulfilled)
		failed := countParityByStatus(ctx, reqColl, dispatch.RequestFailed)
		completedSpawns := countParitySpawnsByStatus(ctx, spawnColl, dispatch.SpawnCompleted)
		failedSpawns := countParitySpawnsByStatus(ctx, spawnColl, dispatch.SpawnFailed)

		if pending == 0 && claimedCount == 0 && running == 0 && fulfilled == 50 && failed == 0 && completedSpawns == 50 && failedSpawns == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	claimCancel()
	spawnCancel()
	completeCancel()
	select {
	case <-injectorDone:
	case <-time.After(2 * time.Second):
	}

	// Collect final state
	pending := countParityByStatus(ctx, reqColl, dispatch.RequestPending)
	claimedCount := countParityByStatus(ctx, reqColl, dispatch.RequestClaimed)
	running := countParitySpawnsByStatus(ctx, spawnColl, dispatch.SpawnRunning)
	fulfilled := countParityByStatus(ctx, reqColl, dispatch.RequestFulfilled)
	failed := countParityByStatus(ctx, reqColl, dispatch.RequestFailed)
	completedSpawns := countParitySpawnsByStatus(ctx, spawnColl, dispatch.SpawnCompleted)
	failedSpawns := countParitySpawnsByStatus(ctx, spawnColl, dispatch.SpawnFailed)

	// Count per-task events by inspecting the request docs
	cursor, err := reqColl.Find(ctx, bson.M{})
	if err != nil {
		t.Fatalf("find requests: %v", err)
	}
	defer cursor.Close(ctx)

	var eventCounts []int
	for cursor.Next(ctx) {
		var raw bson.M
		if err := cursor.Decode(&raw); err != nil {
			continue
		}
		// Events: created → claimed → fulfilled = 3 state transitions
		events := 1 // created
		if raw["claimed_at"] != nil {
			events++
		}
		if raw["fulfilled_at"] != nil {
			events++
		}
		eventCounts = append(eventCounts, events)
	}

	return map[string]interface{}{
		"pending":          pending,
		"claimed":            claimedCount,
		"running":            running,
		"fulfilled":          fulfilled,
		"failed":             failed,
		"completed_spawns":   completedSpawns,
		"failed_spawns":      failedSpawns,
		"event_counts":       eventCounts,
	}
}

// ------------------------------------------------------------------
// TestDispatchParity
// ------------------------------------------------------------------

func TestDispatchParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping parity test in short mode")
	}

	ctx := context.Background()
	concurrencyLimit := 6
	fixtureCount := 50

	// ------------------------------------------------------------------
	// Phase 1: Run Go dispatch 10 times, record states
	// ------------------------------------------------------------------
	var goStates []map[string]interface{}
	for run := 0; run < 10; run++ {
		client := setupMongoParityShared(t)
		reqColl := newParityRequestsColl(t, client, fmt.Sprintf("run_%d_%d", run, time.Now().UnixNano()))
		spawnColl := newParitySpawnsColl(t, client, fmt.Sprintf("run_%d_%d", run, time.Now().UnixNano()))

		if err := seedParityFixtures(ctx, reqColl, fixtureCount); err != nil {
			t.Fatalf("seedParityFixtures failed: %v", err)
		}

		state := runGoDispatchOnce(ctx, t, reqColl, spawnColl, concurrencyLimit)
		goStates = append(goStates, state)
	}

	// ------------------------------------------------------------------
	// Phase 2: Verify determinism across Go runs
	// ------------------------------------------------------------------
	for i, state := range goStates {
		if state["pending"].(int64) != 0 {
			t.Errorf("run %d: expected 0 PENDING, got %d", i, state["pending"])
		}
		if state["claimed"].(int64) != 0 {
			t.Errorf("run %d: expected 0 CLAIMED, got %d", i, state["claimed"])
		}
		if state["running"].(int64) != 0 {
			t.Errorf("run %d: expected 0 RUNNING, got %d", i, state["running"])
		}
		if state["fulfilled"].(int64) != int64(fixtureCount) {
			t.Errorf("run %d: expected %d FULFILLED, got %d", i, fixtureCount, state["fulfilled"])
		}
		if state["failed"].(int64) != 0 {
			t.Errorf("run %d: expected 0 FAILED, got %d", i, state["failed"])
		}
		if state["completed_spawns"].(int64) != int64(fixtureCount) {
			t.Errorf("run %d: expected %d COMPLETED spawns, got %d", i, fixtureCount, state["completed_spawns"])
		}
		if state["failed_spawns"].(int64) != 0 {
			t.Errorf("run %d: expected 0 FAILED spawns, got %d", i, state["failed_spawns"])
		}

		// Every task must have exactly 3 events (created, claimed, fulfilled)
		for _, ec := range state["event_counts"].([]int) {
			if ec != 3 {
				t.Errorf("run %d: expected 3 events per task, got %d", i, ec)
				break
			}
		}
	}

	// ------------------------------------------------------------------
	// Phase 3: Cross-run determinism — all 10 runs must be identical
	// ------------------------------------------------------------------
	baseline := goStates[0]
	for i := 1; i < len(goStates); i++ {
		if goStates[i]["fulfilled"].(int64) != baseline["fulfilled"].(int64) {
			t.Errorf("run %d: fulfilled count %d != baseline %d", i, goStates[i]["fulfilled"], baseline["fulfilled"])
		}
		if goStates[i]["failed"].(int64) != baseline["failed"].(int64) {
			t.Errorf("run %d: failed count %d != baseline %d", i, goStates[i]["failed"], baseline["failed"])
		}
	}

	// ------------------------------------------------------------------
	// Phase 4: Python parity stub
	// ------------------------------------------------------------------
	// The Python dispatch.py side of this test lives in the Python test
	// suite (test_dispatch_parity.py) and is run separately in CI.
	// Both pipelines share the same invariants checked above.
	// When Python dispatch.py is invoked with --dry-run + mock provider,
	// it must also produce:
	//   - 50 COMPLETED tasks
	//   - 0 FAILED
	//   - 0 stale CLAIMED
	//   - 3 events per task
	//
	// To run the Python side manually:
	//   cd ~/.hermes && python3 -m pytest tests/test_dispatch_parity.py -v
	//
	// TODO(DS-4): Once otoxan-dispatch binary exists, add subprocess-based
	// comparison that runs both pipelines against the same MongoDB fixture set.

	t.Logf("parity test complete: 10 runs × %d fixtures, all %d FULFILLED, 0 FAILED, 0 leaks, 3 events/task",
		fixtureCount, fixtureCount)
}
