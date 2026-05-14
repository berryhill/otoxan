package index

import (
	"strings"
	"testing"

	"github.com/silas/otoxan/internal/store/directives"
	"github.com/silas/otoxan/internal/store/flows"
	"github.com/silas/otoxan/internal/store/notifications"
	"github.com/silas/otoxan/internal/store/plans"
	"github.com/silas/otoxan/internal/store/queue"
	"github.com/silas/otoxan/internal/store/reports"
	"github.com/silas/otoxan/internal/store/tasks"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// ------------------------------------------------------------------
// Plan
// ------------------------------------------------------------------

func TestExtractors_Plan(t *testing.T) {
	plan := &plans.Plan{
		PlanID:  "plan-001",
		Title:   "Fix dispatch reaper bug",
		Status:  plans.StatusExecuting,
		Content: "The dispatch reaper is failing to clean up stale tasks.\nRoot cause: missing index on claimed_at.",
		Tags:    []string{"bug", "dispatch"},
	}
	text := ExtractPlan(plan)
	want := []string{
		"Plan: Fix dispatch reaper bug",
		"Status: EXECUTING",
		"The dispatch reaper is failing to clean up stale tasks.",
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("plan extract missing %q\ngot:\n%s", w, text)
		}
	}
	// Deterministic: same input = same output
	if ExtractPlan(plan) != text {
		t.Error("plan extract not deterministic")
	}
}

func TestExtractors_PlanFromBSON(t *testing.T) {
	doc := bson.M{
		"plan_id": "plan-002",
		"title":   "Onboard new agent",
		"status":  "PLANNING",
		"content": "Set up credentials and permissions.",
	}
	text := PlanExtractor().Extract(doc)
	want := "Plan: Onboard new agent\nStatus: PLANNING\nSet up credentials and permissions."
	if text != want {
		t.Errorf("plan bson extract mismatch\nwant:\n%s\ngot:\n%s", want, text)
	}
}

// ------------------------------------------------------------------
// Task
// ------------------------------------------------------------------

func TestExtractors_Task(t *testing.T) {
	directive := "Refactor the indexer to use batching"
	output := "All tests pass after refactor"
	task := &tasks.Task{
		TaskID:         "task-042",
		Title:          "Add batch embed support",
		Status:         tasks.StatusCompleted,
		Description:    "Implement BatchEmbed in the embedder interface.",
		Directive:      &directive,
		Intent:         "Reduce embedding latency via batching",
		Implementation: "Added BatchEmbed(ctx, []string) to Embedder",
		Output:         &output,
		Assignee:       "silas",
	}
	text := ExtractTask(task)
	want := []string{
		"Task: Add batch embed support",
		"Status: COMPLETED",
		"Description: Implement BatchEmbed in the embedder interface.",
		"Directive: Refactor the indexer to use batching",
		"Intent: Reduce embedding latency via batching",
		"Implementation: Added BatchEmbed(ctx, []string) to Embedder",
		"Output: All tests pass after refactor",
		"Assignee: silas",
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("task extract missing %q\ngot:\n%s", w, text)
		}
	}
	if ExtractTask(task) != text {
		t.Error("task extract not deterministic")
	}
}

func TestExtractors_TaskFromBSON(t *testing.T) {
	doc := bson.M{
		"task_id":        "task-043",
		"title":          "Fix flaky test",
		"status":         "FAILED",
		"description":    "The test fails intermittently on CI.",
		"intent":         "Stabilize CI",
		"implementation": "Added retry logic.",
		"assignee":       "archer",
	}
	text := TaskExtractor().Extract(doc)
	want := []string{
		"Task: Fix flaky test",
		"Status: FAILED",
		"Description: The test fails intermittently on CI.",
		"Intent: Stabilize CI",
		"Implementation: Added retry logic.",
		"Assignee: archer",
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("task bson extract missing %q\ngot:\n%s", w, text)
		}
	}
}

// ------------------------------------------------------------------
// Report
// ------------------------------------------------------------------

func TestExtractors_Report(t *testing.T) {
	report := &reports.Report{
		ReportID: "rep-001",
		Title:    "Weekly Index Health",
		Status:   reports.StatusPublished,
		Content:  "Qdrant collection has 12,400 points.\nNo stale pointers detected.",
		Tags:     []string{"ops", "index"},
	}
	text := ExtractReport(report)
	want := []string{
		"Report: Weekly Index Health",
		"Status: PUBLISHED",
		"Qdrant collection has 12,400 points.",
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("report extract missing %q\ngot:\n%s", w, text)
		}
	}
	if ExtractReport(report) != text {
		t.Error("report extract not deterministic")
	}
}

// ------------------------------------------------------------------
// Directive
// ------------------------------------------------------------------

func TestExtractors_Directive(t *testing.T) {
	d := &directives.Directive{
		DirectiveID: "dir-001",
		Title:       "Never inline embed",
		Content:     "Embedding must happen in a separate process to avoid write latency.",
		Category:    "performance",
		Enabled:     true,
	}
	text := ExtractDirective(d)
	want := []string{
		"Directive: Never inline embed",
		"Category: performance",
		"Embedding must happen in a separate process to avoid write latency.",
		"Enabled: true",
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("directive extract missing %q\ngot:\n%s", w, text)
		}
	}
	if ExtractDirective(d) != text {
		t.Error("directive extract not deterministic")
	}
}

// ------------------------------------------------------------------
// TaskEvent
// ------------------------------------------------------------------

func TestExtractors_TaskEvent(t *testing.T) {
	event := &queue.TaskEvent{
		TaskID:    "task-042",
		EventType: "completed",
		Actor:     "silas",
		Data: bson.M{
			"duration_ms": 1200,
			"result":      "success",
		},
	}
	text := ExtractTaskEvent(event)
	want := []string{
		"Event: completed",
		"Task: task-042",
		"Actor: silas",
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("task_event extract missing %q\ngot:\n%s", w, text)
		}
	}
	if ExtractTaskEvent(event) != text {
		t.Error("task_event extract not deterministic")
	}
}

// ------------------------------------------------------------------
// Notification
// ------------------------------------------------------------------

func TestExtractors_Notification(t *testing.T) {
	n := &notifications.Notification{
		NotificationID: "notif-001",
		Title:          "Task completed",
		Body:           "Task task-042 finished successfully.",
		Channel:        notifications.ChannelSlack,
		Status:         notifications.StatusSent,
	}
	text := ExtractNotification(n)
	want := []string{
		"Notification: Task completed",
		"Task task-042 finished successfully.",
		"Channel: slack",
		"Status: SENT",
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("notification extract missing %q\ngot:\n%s", w, text)
		}
	}
	if ExtractNotification(n) != text {
		t.Error("notification extract not deterministic")
	}
}

// ------------------------------------------------------------------
// Flow
// ------------------------------------------------------------------

func TestExtractors_Flow(t *testing.T) {
	f := &flows.Flow{
		FlowID:      "flow-001",
		Name:        "GitHub Issue → PR",
		Description: "Intake an issue, write code, open a PR.",
		Status:      flows.StatusActive,
	}
	text := ExtractFlow(f)
	want := []string{
		"Flow: GitHub Issue → PR",
		"Intake an issue, write code, open a PR.",
		"Status: ACTIVE",
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("flow extract missing %q\ngot:\n%s", w, text)
		}
	}
	if ExtractFlow(f) != text {
		t.Error("flow extract not deterministic")
	}
}

// ------------------------------------------------------------------
// Session
// ------------------------------------------------------------------

func TestExtractors_Session(t *testing.T) {
	doc := bson.M{
		"session_id":         "sess-001",
		"user_content":       "How do I fix the reaper bug?",
		"assistant_content":  "Add an index on claimed_at.",
	}
	text := SessionExtractor().Extract(doc)
	want := []string{
		"User: How do I fix the reaper bug?",
		"Assistant: Add an index on claimed_at.",
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("session extract missing %q\ngot:\n%s", w, text)
		}
	}
	if SessionExtractor().Extract(doc) != text {
		t.Error("session extract not deterministic")
	}
}

// ------------------------------------------------------------------
// Build
// ------------------------------------------------------------------

func TestExtractors_Build(t *testing.T) {
	doc := bson.M{
		"build_id":    "build-001",
		"output":      "PASS\nok  github.com/silas/otoxan/internal/index",
		"error_trace": "",
		"status":      "success",
	}
	text := BuildExtractor().Extract(doc)
	want := []string{
		"Build Output:\nPASS",
		"Status: success",
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("build extract missing %q\ngot:\n%s", w, text)
		}
	}
	if BuildExtractor().Extract(doc) != text {
		t.Error("build extract not deterministic")
	}
}

// ------------------------------------------------------------------
// Error
// ------------------------------------------------------------------

func TestExtractors_Error(t *testing.T) {
	doc := bson.M{
		"error_id":    "err-001",
		"message":     "connection refused to qdrant",
		"stack_trace": "net.Dial: connection refused\n    at internal/qdrant/client.go:45",
	}
	text := ErrorExtractor().Extract(doc)
	want := []string{
		"Error: connection refused to qdrant",
		"Stack Trace:\nnet.Dial: connection refused",
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("error extract missing %q\ngot:\n%s", w, text)
		}
	}
	if ErrorExtractor().Extract(doc) != text {
		t.Error("error extract not deterministic")
	}
}

// ------------------------------------------------------------------
// Run
// ------------------------------------------------------------------

func TestExtractors_Run(t *testing.T) {
	doc := bson.M{
		"run_id":        "run-001",
		"status":        "failed",
		"error_message": "timeout waiting for indexer",
		"output":        "Indexed 42 documents",
	}
	text := RunExtractor().Extract(doc)
	want := []string{
		"Run Status: failed",
		"Error: timeout waiting for indexer",
		"Output: Indexed 42 documents",
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("run extract missing %q\ngot:\n%s", w, text)
		}
	}
	if RunExtractor().Extract(doc) != text {
		t.Error("run extract not deterministic")
	}
}

// ------------------------------------------------------------------
// Determinism across all extractors with known fixtures
// ------------------------------------------------------------------

func TestExtractors(t *testing.T) {
	// This test runs every extractor against a known fixture and verifies
	// deterministic output.
	fixtures := []struct {
		name      string
		extractor Extractor
		doc       bson.M
		want      string
	}{
		{
			name:      "plan",
			extractor: PlanExtractor(),
			doc: bson.M{
				"title":   "Test Plan",
				"status":  "PLANNING",
				"content": "Plan content here.",
			},
			want: "Plan: Test Plan\nStatus: PLANNING\nPlan content here.",
		},
		{
			name:      "task",
			extractor: TaskExtractor(),
			doc: bson.M{
				"title":         "Test Task",
				"status":        "PENDING",
				"description":   "Do the thing.",
				"intent":        "Make it work.",
				"implementation": "Changed the code.",
			},
			want: "Task: Test Task\nStatus: PENDING\nDescription: Do the thing.\nIntent: Make it work.\nImplementation: Changed the code.",
		},
		{
			name:      "report",
			extractor: ReportExtractor(),
			doc: bson.M{
				"title":   "Test Report",
				"status":  "DRAFT",
				"content": "Report content.",
			},
			want: "Report: Test Report\nStatus: DRAFT\nReport content.",
		},
		{
			name:      "directive",
			extractor: DirectiveExtractor(),
			doc: bson.M{
				"title":    "Test Directive",
				"category": "testing",
				"content":  "Directive content.",
				"enabled":  true,
			},
			want: "Directive: Test Directive\nCategory: testing\nDirective content.\nEnabled: true",
		},
		{
			name:      "task_event",
			extractor: TaskEventExtractor(),
			doc: bson.M{
				"event_type": "started",
				"task_id":    "task-001",
				"actor":      "silas",
			},
			want: "Event: started\nTask: task-001\nActor: silas",
		},
		{
			name:      "notification",
			extractor: NotificationExtractor(),
			doc: bson.M{
				"title":   "Hello",
				"body":    "World",
				"channel": "in_app",
				"status":  "PENDING",
			},
			want: "Notification: Hello\nWorld\nChannel: in_app\nStatus: PENDING",
		},
		{
			name:      "flow",
			extractor: FlowExtractor(),
			doc: bson.M{
				"name":        "Test Flow",
				"description": "A test flow.",
				"status":      "DRAFT",
			},
			want: "Flow: Test Flow\nA test flow.\nStatus: DRAFT",
		},
		{
			name:      "session",
			extractor: SessionExtractor(),
			doc: bson.M{
				"user_content":      "What is 2+2?",
				"assistant_content": "4",
			},
			want: "User: What is 2+2?\nAssistant: 4",
		},
		{
			name:      "build",
			extractor: BuildExtractor(),
			doc: bson.M{
				"output": "Build succeeded.",
				"status": "success",
			},
			want: "Build Output:\nBuild succeeded.\nStatus: success",
		},
		{
			name:      "error",
			extractor: ErrorExtractor(),
			doc: bson.M{
				"message":     "Something broke.",
				"stack_trace": "at foo.go:12",
			},
			want: "Error: Something broke.\nStack Trace:\nat foo.go:12",
		},
		{
			name:      "run",
			extractor: RunExtractor(),
			doc: bson.M{
				"status":        "completed",
				"error_message": "",
				"output":        "Done.",
			},
			want: "Run Status: completed\nOutput: Done.",
		},
	}

	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.extractor.Extract(tc.doc)
			if got != tc.want {
				t.Errorf("extract mismatch\nwant:\n%s\ngot:\n%s", tc.want, got)
			}
			// Determinism
			if tc.extractor.Extract(tc.doc) != got {
				t.Error("extract not deterministic")
			}
		})
	}
}
