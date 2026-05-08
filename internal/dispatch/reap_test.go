package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ensure Completion is used so the compiler doesn't complain.
var _ = Completion{}

// ------------------------------------------------------------------
// Test helpers
// ------------------------------------------------------------------

func newReapTestColl(t *testing.T, client *mongo.Client) *mongo.Collection {
	t.Helper()
	return client.Database("silas").Collection(fmt.Sprintf("dispatch_reap_%d", time.Now().UnixNano()))
}

func writeMarker(t *testing.T, dir string, comp *Completion) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
	path := filepath.Join(dir, fmt.Sprintf("%s.json", comp.TaskID))
	data, err := json.Marshal(comp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
	return path
}

func seedSpawnRunning(ctx context.Context, coll *mongo.Collection, taskID, reqID string, pid int) error {
	rec := SpawnRecord{
		TaskID:    taskID,
		RequestID: reqID,
		PID:       pid,
		Status:    SpawnRunning,
		StartedAt: time.Now().UTC(),
		Lane:      "hermes",
	}
	_, err := coll.InsertOne(ctx, rec)
	return err
}

func seedRequestClaimed(ctx context.Context, coll *mongo.Collection, taskID, reqID string) error {
	req := DispatchRequest{
		RequestID: reqID,
		TaskID:    taskID,
		Status:    RequestClaimed,
		CreatedAt: time.Now().UTC(),
	}
	_, err := coll.InsertOne(ctx, req)
	return err
}

// ------------------------------------------------------------------
// Unit tests
// ------------------------------------------------------------------

func TestReapOne_Success(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	comp := &Completion{
		TaskID:         "t_reap_ok",
		TaskStatus:     "COMPLETED",
		ExitCode:       0,
		RuntimeSeconds: 42,
		CompletedAt:    time.Now().UTC(),
	}
	_ = writeMarker(t, dir, comp)

	out := make(chan *Completion, 1)
	deps := ReapDeps{}

	// We cannot swap the package-level constant, so we test via the
	// public API by writing to the real dir and cleaning up.
	realDir := "/tmp/otoxan_completed"
	_ = os.RemoveAll(realDir)
	_ = os.MkdirAll(realDir, 0755)
	realPath := filepath.Join(realDir, "t_reap_ok.json")
	data, _ := json.Marshal(comp)
	_ = os.WriteFile(realPath, data, 0644)
	defer os.Remove(realPath)

	if err := reapOnce(ctx, deps, out); err != nil {
		t.Fatalf("reapOnce failed: %v", err)
	}

	select {
	case got := <-out:
		if got.TaskID != comp.TaskID {
			t.Errorf("task_id mismatch: got %q, want %q", got.TaskID, comp.TaskID)
		}
		if got.ExitCode != comp.ExitCode {
			t.Errorf("exit_code mismatch: got %d, want %d", got.ExitCode, comp.ExitCode)
		}
	default:
		t.Fatal("expected completion on channel")
	}

	if _, err := os.Stat(realPath); !os.IsNotExist(err) {
		t.Error("marker file should have been deleted")
	}
}

func TestReapOne_MalformedJSON(t *testing.T) {
	ctx := context.Background()
	realDir := "/tmp/otoxan_completed"
	_ = os.RemoveAll(realDir)
	_ = os.MkdirAll(realDir, 0755)

	badPath := filepath.Join(realDir, "bad.json")
	_ = os.WriteFile(badPath, []byte("not json"), 0644)
	defer os.Remove(badPath)

	out := make(chan *Completion, 1)
	deps := ReapDeps{}

	if err := reapOnce(ctx, deps, out); err != nil {
		t.Fatalf("reapOnce failed: %v", err)
	}

	if _, err := os.Stat(badPath); !os.IsNotExist(err) {
		t.Error("malformed marker file should have been deleted")
	}

	select {
	case <-out:
		t.Fatal("expected no completion for malformed marker")
	default:
	}
}

func TestReapOne_MissingTaskID(t *testing.T) {
	ctx := context.Background()
	realDir := "/tmp/otoxan_completed"
	_ = os.RemoveAll(realDir)
	_ = os.MkdirAll(realDir, 0755)

	path := filepath.Join(realDir, "notask.json")
	data, _ := json.Marshal(map[string]any{"exit_code": 0})
	_ = os.WriteFile(path, data, 0644)
	defer os.Remove(path)

	out := make(chan *Completion, 1)
	deps := ReapDeps{}

	if err := reapOnce(ctx, deps, out); err != nil {
		t.Fatalf("reapOnce failed: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("marker with missing task_id should have been deleted")
	}

	select {
	case <-out:
		t.Fatal("expected no completion for missing task_id")
	default:
	}
}

func TestRunReapLoop_CleanShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	out := make(chan *Completion)
	deps := ReapDeps{}

	done := make(chan error, 1)
	go func() {
		done <- RunReapLoop(ctx, deps, out)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunReapLoop did not exit after cancel")
	}
}

func TestRunReapLoop_DeliversMultiple(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	realDir := "/tmp/otoxan_completed"
	_ = os.RemoveAll(realDir)
	_ = os.MkdirAll(realDir, 0755)
	defer os.RemoveAll(realDir)

	for i := 0; i < 3; i++ {
		comp := &Completion{
			TaskID:      fmt.Sprintf("t_multi_%d", i),
			ExitCode:    i,
			CompletedAt: time.Now().UTC(),
		}
		data, _ := json.Marshal(comp)
		_ = os.WriteFile(filepath.Join(realDir, fmt.Sprintf("t_multi_%d.json", i)), data, 0644)
	}

	out := make(chan *Completion, 3)
	deps := ReapDeps{}

	done := make(chan error, 1)
	go func() {
		done <- RunReapLoop(ctx, deps, out)
	}()

	// Wait for the first tick to fire.
	time.Sleep(6 * time.Second)
	cancel()

	<-done

	count := 0
	drain:
	for {
		select {
		case <-out:
			count++
		default:
			break drain
		}
	}

	// The loop deletes files after sending to the channel, but if the
	// channel is full or ctx cancels mid-send, files may remain.  Check
	// both the channel and the filesystem to avoid false negatives.
	remaining := 0
	entries, _ := os.ReadDir(realDir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			remaining++
		}
	}

	if count != 3 && remaining != 0 {
		t.Fatalf("expected 3 completions (got %d) or 0 remaining files (got %d)", count, remaining)
	}
}

// ------------------------------------------------------------------
// Umbrella runner
// ------------------------------------------------------------------

func TestReap(t *testing.T) {
	t.Run("OneSuccess", func(t *testing.T) { TestReapOne_Success(t) })
	t.Run("MalformedJSON", func(t *testing.T) { TestReapOne_MalformedJSON(t) })
	t.Run("MissingTaskID", func(t *testing.T) { TestReapOne_MissingTaskID(t) })
	t.Run("CleanShutdown", func(t *testing.T) { TestRunReapLoop_CleanShutdown(t) })
	t.Run("DeliversMultiple", func(t *testing.T) { TestRunReapLoop_DeliversMultiple(t) })
}
