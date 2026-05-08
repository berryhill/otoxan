package dispatch

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDispatchRequestBSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	claimed := now.Add(-5 * time.Minute)

	original := DispatchRequest{
		RequestID: "dr_abc123",
		TaskID:    "t_xyz789",
		Status:    RequestClaimed,
		CreatedAt: now,
		ClaimedAt: &claimed,
		Priority:  1,
		Error:     "",
		Extra: map[string]any{
			"foo": "bar",
		},
	}

	var decoded DispatchRequest
	if err := BSONRoundTrip(&original, &decoded); err != nil {
		t.Fatalf("BSON round-trip failed: %v", err)
	}

	if decoded.RequestID != original.RequestID {
		t.Errorf("RequestID mismatch: got %q, want %q", decoded.RequestID, original.RequestID)
	}
	if decoded.TaskID != original.TaskID {
		t.Errorf("TaskID mismatch: got %q, want %q", decoded.TaskID, original.TaskID)
	}
	if decoded.Status != original.Status {
		t.Errorf("Status mismatch: got %q, want %q", decoded.Status, original.Status)
	}
	if !decoded.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("CreatedAt mismatch: got %v, want %v", decoded.CreatedAt, original.CreatedAt)
	}
	if decoded.ClaimedAt == nil {
		t.Fatal("ClaimedAt is nil, expected non-nil")
	}
	if !decoded.ClaimedAt.Equal(claimed) {
		t.Errorf("ClaimedAt mismatch: got %v, want %v", *decoded.ClaimedAt, claimed)
	}
	if decoded.Priority != original.Priority {
		t.Errorf("Priority mismatch: got %d, want %d", decoded.Priority, original.Priority)
	}
	if decoded.Extra == nil {
		t.Fatal("Extra is nil, expected map with foo=bar")
	}
	if decoded.Extra["foo"] != "bar" {
		t.Errorf("Extra.foo mismatch: got %v, want bar", decoded.Extra["foo"])
	}
}

func TestDispatchRequestOptionalFieldsOmitted(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	original := DispatchRequest{
		RequestID: "dr_pending01",
		TaskID:    "t_task01",
		Status:    RequestPending,
		CreatedAt: now,
		Priority:  2,
	}

	data, err := bson.Marshal(&original)
	if err != nil {
		t.Fatalf("BSON marshal failed: %v", err)
	}

	raw := bson.Raw(data)
	if _, err := raw.LookupErr("claimed_at"); err == nil {
		t.Error("claimed_at should be omitted for nil pointer")
	}
	if _, err := raw.LookupErr("fulfilled_at"); err == nil {
		t.Error("fulfilled_at should be omitted for nil pointer")
	}
	if _, err := raw.LookupErr("error"); err == nil {
		t.Error("error should be omitted for empty string")
	}

	var decoded DispatchRequest
	if err := bson.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("BSON unmarshal failed: %v", err)
	}
	if decoded.ClaimedAt != nil {
		t.Errorf("ClaimedAt should be nil, got %v", *decoded.ClaimedAt)
	}
	if decoded.FulfilledAt != nil {
		t.Errorf("FulfilledAt should be nil, got %v", *decoded.FulfilledAt)
	}
}

func TestCompletionBSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	original := Completion{
		TaskID:         "t_abc123",
		TaskStatus:     "COMPLETED",
		Output:         "All done",
		ExitCode:       0,
		RuntimeSeconds: 145,
		ErrorSummary:   "",
		LastLogLines:   []string{"line1", "line2"},
		SessionID:      "dispatch_dr_xyz_abc123",
		CompletedAt:    now,
	}

	var decoded Completion
	if err := BSONRoundTrip(&original, &decoded); err != nil {
		t.Fatalf("BSON round-trip failed: %v", err)
	}

	if decoded.TaskID != original.TaskID {
		t.Errorf("TaskID mismatch: got %q, want %q", decoded.TaskID, original.TaskID)
	}
	if decoded.TaskStatus != original.TaskStatus {
		t.Errorf("TaskStatus mismatch: got %q, want %q", decoded.TaskStatus, original.TaskStatus)
	}
	if decoded.Output != original.Output {
		t.Errorf("Output mismatch: got %q, want %q", decoded.Output, original.Output)
	}
	if decoded.ExitCode != original.ExitCode {
		t.Errorf("ExitCode mismatch: got %d, want %d", decoded.ExitCode, original.ExitCode)
	}
	if decoded.RuntimeSeconds != original.RuntimeSeconds {
		t.Errorf("RuntimeSeconds mismatch: got %d, want %d", decoded.RuntimeSeconds, original.RuntimeSeconds)
	}
	if len(decoded.LastLogLines) != 2 || decoded.LastLogLines[0] != "line1" {
		t.Errorf("LastLogLines mismatch: got %v", decoded.LastLogLines)
	}
	if !decoded.CompletedAt.Equal(original.CompletedAt) {
		t.Errorf("CompletedAt mismatch: got %v, want %v", decoded.CompletedAt, original.CompletedAt)
	}
}

func TestSpawnRecordBSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	original := SpawnRecord{
		TaskID:         "t_spawn01",
		RequestID:      "dr_rq01",
		SessionID:      "sess_001",
		PID:            12345,
		StartedAt:      now,
		Status:         SpawnRunning,
		ExitCode:       0,
		TaskStatus:     "RUNNING",
		LogTail:        []string{"init"},
		RuntimeSeconds: 0,
		ErrorSummary:   "",
		Lane:           "hermes",
	}

	var decoded SpawnRecord
	if err := BSONRoundTrip(&original, &decoded); err != nil {
		t.Fatalf("BSON round-trip failed: %v", err)
	}

	if decoded.TaskID != original.TaskID {
		t.Errorf("TaskID mismatch")
	}
	if decoded.Status != original.Status {
		t.Errorf("Status mismatch: got %q, want %q", decoded.Status, original.Status)
	}
	if decoded.PID != original.PID {
		t.Errorf("PID mismatch: got %d, want %d", decoded.PID, original.PID)
	}
}
func TestTypes(t *testing.T) {
	// Run all type-level tests under a single umbrella so -run TestTypes matches.
	t.Run("DispatchRequestBSONRoundTrip", func(t *testing.T) { TestDispatchRequestBSONRoundTrip(t) })
	t.Run("DispatchRequestOptionalFieldsOmitted", func(t *testing.T) { TestDispatchRequestOptionalFieldsOmitted(t) })
	t.Run("CompletionBSONRoundTrip", func(t *testing.T) { TestCompletionBSONRoundTrip(t) })
	t.Run("SpawnRecordBSONRoundTrip", func(t *testing.T) { TestSpawnRecordBSONRoundTrip(t) })
	t.Run("RequestStatusString", func(t *testing.T) { TestRequestStatusString(t) })
	t.Run("SpawnStatusString", func(t *testing.T) { TestSpawnStatusString(t) })
}

func TestRequestStatusString(t *testing.T) {
	cases := []struct {
		status RequestStatus
		want   string
	}{
		{RequestPending, "PENDING"},
		{RequestClaimed, "CLAIMED"},
		{RequestFulfilled, "FULFILLED"},
		{RequestFailed, "FAILED"},
		{RequestDropped, "DROPPED"},
		{RequestCancelled, "CANCELLED"},
	}
	for _, c := range cases {
		if string(c.status) != c.want {
			t.Errorf("RequestStatus(%q) = %q, want %q", c.status, c.status, c.want)
		}
	}
}

func TestSpawnStatusString(t *testing.T) {
	cases := []struct {
		status SpawnStatus
		want   string
	}{
		{SpawnRunning, "RUNNING"},
		{SpawnCompleted, "COMPLETED"},
		{SpawnFailed, "FAILED"},
	}
	for _, c := range cases {
		if string(c.status) != c.want {
			t.Errorf("SpawnStatus(%q) = %q, want %q", c.status, c.status, c.want)
		}
	}
}
