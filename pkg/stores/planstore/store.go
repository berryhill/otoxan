// Package planstore provides a MongoDB-backed store for plan documents.
//
// It is the public-facing API that wraps internal/store/plans and adds
// cross-store query methods (execution status, stuck detection, task
// extraction) that need the task queue. Method signatures map 1:1 with
// the Python planstore.py public surface.
package planstore

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/silas/otoxan/internal/state"
	"github.com/silas/otoxan/internal/store/plans"
	"github.com/silas/otoxan/pkg/stores/taskqueue"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ------------------------------------------------------------------
// Re-exported types (public API surface)
// ------------------------------------------------------------------

// PlanStatus enumerates the possible states of a plan.
type PlanStatus = plans.PlanStatus

// PlanType enumerates the kinds of plans.
type PlanType = plans.PlanType

// Plan is the canonical BSON shape for a plan document.
type Plan = plans.Plan

const (
	StatusPlanning  = plans.StatusPlanning
	StatusExecuting = plans.StatusExecuting
	StatusPaused    = plans.StatusPaused
	StatusCompleted = plans.StatusCompleted
	StatusAbandoned = plans.StatusAbandoned
	StatusChecking  = plans.StatusChecking
	StatusAccepted  = plans.StatusAccepted
	StatusRegressed = plans.StatusRegressed
)

const (
	TypeStandard = plans.TypeStandard
	TypeQAFirst  = plans.TypeQAFirst
	TypeHotfix   = plans.TypeHotfix
)

// ------------------------------------------------------------------
// Store construction
// ------------------------------------------------------------------

// Store is the public-facing MongoDB-backed store for plans. It wraps the
// internal plans store and adds cross-store query methods that need the
// task queue for ground truth.
type Store struct {
	agentName string
	plans     *plans.PlanStore
	tasks     *taskqueue.Store
}

// NewStore creates a plan store for the named agent. It resolves the
// per-agent DB for plans and also opens the task queue store so that
// cross-store methods (GetExecutionStatus, FindStuck, etc.) work.
func NewStore(client *mongo.Client, agentName string) (*Store, error) {
	if err := state.ValidateAgentName(agentName); err != nil {
		return nil, err
	}
	db, err := state.AgentDB(client, agentName)
	if err != nil {
		return nil, err
	}
	tq, err := taskqueue.NewStore(client, agentName)
	if err != nil {
		return nil, fmt.Errorf("taskqueue: %w", err)
	}
	ps := plans.NewPlanStore(db.Collection("plans"))
	return &Store{
		agentName: agentName,
		plans:     ps,
		tasks:     tq,
	}, nil
}

// ------------------------------------------------------------------
// CRUD (delegated to internal plans store)
// ------------------------------------------------------------------

// Create inserts a new plan document.
func (s *Store) Create(ctx context.Context, plan *Plan) (*mongo.InsertOneResult, error) {
	return s.plans.Create(ctx, plan)
}

// Get retrieves a single plan by plan_id.
func (s *Store) Get(ctx context.Context, planID string) (*Plan, error) {
	return s.plans.Get(ctx, planID)
}

// GetWithDeleted retrieves a plan including soft-deleted ones.
func (s *Store) GetWithDeleted(ctx context.Context, planID string) (*Plan, error) {
	return s.plans.GetWithDeleted(ctx, planID)
}

// Update patches fields of an existing plan.
func (s *Store) Update(ctx context.Context, planID string, updates bson.M) (*mongo.UpdateResult, error) {
	return s.plans.Update(ctx, planID, updates)
}

// Delete soft-deletes a plan by plan_id.
func (s *Store) Delete(ctx context.Context, planID string) (*mongo.UpdateResult, error) {
	return s.plans.Delete(ctx, planID)
}

// Restore un-deletes a soft-deleted plan.
func (s *Store) Restore(ctx context.Context, planID string) (*mongo.UpdateResult, error) {
	return s.plans.Restore(ctx, planID)
}

// HardDelete permanently removes a plan.
func (s *Store) HardDelete(ctx context.Context, planID string) (*mongo.DeleteResult, error) {
	return s.plans.HardDelete(ctx, planID)
}

// ------------------------------------------------------------------
// List / Query (delegated or enhanced)
// ------------------------------------------------------------------

// ListOptions configures the List query.
type ListOptions = plans.ListOptions

// List returns plans matching the provided filters.
func (s *Store) List(ctx context.Context, opts ListOptions) ([]Plan, error) {
	return s.plans.List(ctx, opts)
}

// ListByStatus returns plans filtered by a single status, sorted by
// updated_at descending. This is the convenience method that maps
// planstore.py list(status=STATUS). Pass an empty status to list all.
func (s *Store) ListByStatus(ctx context.Context, status PlanStatus, limit int) ([]Plan, error) {
	opts := plans.ListOptions{Limit: limit}
	if status != "" {
		opts.Status = []plans.PlanStatus{status}
	}
	return s.plans.List(ctx, opts)
}

// Count returns the number of plans matching the filter.
func (s *Store) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.plans.Count(ctx, filter)
}

// Archive sets archived_at on a plan.
func (s *Store) Archive(ctx context.Context, planID string) (*mongo.UpdateResult, error) {
	return s.plans.Archive(ctx, planID)
}

// Unarchive clears archived_at on a plan.
func (s *Store) Unarchive(ctx context.Context, planID string) (*mongo.UpdateResult, error) {
	return s.plans.Unarchive(ctx, planID)
}

// ------------------------------------------------------------------
// ExtractTasks — Markdown parser (port of planstore.py extract_tasks)
// ------------------------------------------------------------------

// ExtractedTask holds structured task data parsed from Markdown content.
type ExtractedTask struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Status         string   `json:"status"`
	DependsOn      []string `json:"depends_on"`
	Assigned       *string  `json:"assigned,omitempty"`
	Tool           *string  `json:"tool,omitempty"`
	Verify         *string  `json:"verify,omitempty"`
	ParentProvider *string  `json:"parent_provider,omitempty"`
}

var (
	taskHeadingRe = regexp.MustCompile(`(?m)^###\s+(T\d+):\s+(.*)$`)
	statusRe      = regexp.MustCompile(`(?m)\*\*Status:\*\*\s*(PENDING|RUNNING|SUCCESS|FAILED|SKIPPED)`)
	depsRe        = regexp.MustCompile(`(?m)\*\*Depends on:\*\*\s*(.+)`)
	assignedRe    = regexp.MustCompile(`(?m)\*\*Assigned:\*\*\s*(.+)`)
	toolRe        = regexp.MustCompile(`(?m)\*\*Tool:\*\*\s*(.+)`)
	verifyRe      = regexp.MustCompile(`(?m)\*\*Verify:\*\*\s*(.+)`)
	parentProvRe  = regexp.MustCompile(`(?m)\*\*Parent Provider:\*\*\s*(.+)`)
)

// ExtractTasks extracts structured task data from Markdown plan content.
// It splits the document on '### T<digits>:' headings and parses each
// task block for status, dependencies, assignment, tool, verify, and
// parent_provider fields.
func (s *Store) ExtractTasks(content string) []ExtractedTask {
	var tasks []ExtractedTask

	// Find all task heading positions
	matches := taskHeadingRe.FindAllStringIndex(content, -1)
	if matches == nil {
		return tasks
	}

	for i, match := range matches {
		start := match[0]
		var end int
		if i+1 < len(matches) {
			end = matches[i+1][0]
		} else {
			end = len(content)
		}

		block := content[start:end]
		heading := content[match[0]:match[1]]

		// Parse heading: "### T01: Title text"
		headingParts := taskHeadingRe.FindStringSubmatch(heading)
		if headingParts == nil {
			continue
		}
		taskID := headingParts[1]
		title := strings.TrimSpace(headingParts[2])

		// Parse body fields
		status := "PENDING"
		if m := statusRe.FindStringSubmatch(block); m != nil {
			status = m[1]
		}

		var dependsOn []string
		if m := depsRe.FindStringSubmatch(block); m != nil {
			depsStr := strings.TrimSpace(m[1])
			if depsStr != "(none)" {
				for _, d := range strings.Split(depsStr, ",") {
					d = strings.TrimSpace(d)
					if d != "" {
						dependsOn = append(dependsOn, d)
					}
				}
			}
		}

		var assigned, tool, verify, parentProvider *string
		if m := assignedRe.FindStringSubmatch(block); m != nil {
			v := strings.TrimSpace(m[1])
			assigned = &v
		}
		if m := toolRe.FindStringSubmatch(block); m != nil {
			v := strings.TrimSpace(m[1])
			tool = &v
		}
		if m := verifyRe.FindStringSubmatch(block); m != nil {
			v := strings.TrimSpace(m[1])
			verify = &v
		}
		if m := parentProvRe.FindStringSubmatch(block); m != nil {
			v := strings.TrimSpace(m[1])
			parentProvider = &v
		}

		tasks = append(tasks, ExtractedTask{
			ID:             taskID,
			Title:          title,
			Status:         status,
			DependsOn:      dependsOn,
			Assigned:       assigned,
			Tool:           tool,
			Verify:         verify,
			ParentProvider: parentProvider,
		})
	}

	return tasks
}

// ------------------------------------------------------------------
// GetExecutionStatus — queue ground truth (port of planstore.py get_execution_status)
// ------------------------------------------------------------------

// ExecutionStatus holds the live task progress for a plan.
type ExecutionStatus struct {
	PlanID      string  `json:"plan_id"`
	Total       int     `json:"total"`
	Completed   int     `json:"completed"`
	Failed      int     `json:"failed"`
	Running     int     `json:"running"`
	Queued      int     `json:"queued"`
	Blocked     int     `json:"blocked"`
	Skipped     int     `json:"skipped"`
	Cancelled   int     `json:"cancelled"`
	Pending     int     `json:"pending"`
	PercentDone float64 `json:"percent_done"`
	IsStuck     bool    `json:"is_stuck"`
	StuckReason *string `json:"stuck_reason,omitempty"`
}

// GetExecutionStatus returns execution status for a plan by querying the
// task queue. It never reads stale Markdown status lines from plan content.
func (s *Store) GetExecutionStatus(ctx context.Context, planID string) (*ExecutionStatus, error) {
	tasks, err := s.tasks.ListTasks(ctx, taskqueue.ListTasksOptions{
		PlanID: planID,
		Limit:  10000,
	})
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	total := len(tasks)
	if total == 0 {
		return &ExecutionStatus{
			PlanID:      planID,
			Total:       0,
			PercentDone: 0.0,
		}, nil
	}

	counts := map[taskqueue.TaskStatus]int{
		taskqueue.TaskStatusCompleted: 0,
		taskqueue.TaskStatusFailed:    0,
		taskqueue.TaskStatusRunning:   0,
		taskqueue.TaskStatusQueued:    0,
		taskqueue.TaskStatusBlocked:   0,
		taskqueue.TaskStatusSkipped:   0,
		taskqueue.TaskStatusCancelled: 0,
	}
	for _, t := range tasks {
		if c, ok := counts[t.Status]; ok {
			counts[t.Status] = c + 1
		}
	}

	completed := counts[taskqueue.TaskStatusCompleted]
	pending := total - completed - counts[taskqueue.TaskStatusRunning] -
		counts[taskqueue.TaskStatusFailed] - counts[taskqueue.TaskStatusSkipped] -
		counts[taskqueue.TaskStatusCancelled] - counts[taskqueue.TaskStatusBlocked] -
		counts[taskqueue.TaskStatusQueued]
	if pending < 0 {
		pending = 0
	}

	percentDone := 0.0
	if total > 0 {
		percentDone = float64(completed*100) / float64(total)
	}

	// Stuck detection: EXECUTING plan with 0 RUNNING and 0 QUEUED
	plan, _ := s.plans.Get(ctx, planID)
	isStuck := false
	var stuckReason *string
	if plan != nil && plan.Status == StatusExecuting {
		if counts[taskqueue.TaskStatusRunning] == 0 && counts[taskqueue.TaskStatusQueued] == 0 &&
			counts[taskqueue.TaskStatusBlocked] == 0 {
			isStuck = true
			if counts[taskqueue.TaskStatusFailed] > 0 {
				r := "all_tasks_failed"
				stuckReason = &r
			} else if completed > 0 && completed < total {
				r := "partial_completion_no_runnable"
				stuckReason = &r
			} else if completed == 0 {
				r := "no_tasks_ever_started"
				stuckReason = &r
			}
		}
	}

	return &ExecutionStatus{
		PlanID:      planID,
		Total:       total,
		Completed:   completed,
		Failed:      counts[taskqueue.TaskStatusFailed],
		Running:     counts[taskqueue.TaskStatusRunning],
		Queued:      counts[taskqueue.TaskStatusQueued],
		Blocked:     counts[taskqueue.TaskStatusBlocked],
		Skipped:     counts[taskqueue.TaskStatusSkipped],
		Cancelled:   counts[taskqueue.TaskStatusCancelled],
		Pending:     pending,
		PercentDone: percentDone,
		IsStuck:     isStuck,
		StuckReason: stuckReason,
	}, nil
}

// ------------------------------------------------------------------
// FindStuck — stuck plan detection (port of planstore.py find_stuck_plans)
// ------------------------------------------------------------------

// StuckPlan is a plan enriched with stuck-reason and queue summary.
type StuckPlan struct {
	Plan         *Plan           `json:"plan"`
	StuckReason  string          `json:"stuck_reason"`
	QueueSummary QueueSummary    `json:"queue_summary"`
	LastUpdated  time.Time       `json:"last_updated"`
}

// QueueSummary holds task counts for stuck-plan reporting.
type QueueSummary struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Blocked   int `json:"blocked"`
	Queued    int `json:"queued"`
	Running   int `json:"running"`
}

// FindStuck finds plans that appear stuck in EXECUTING status.
//
// A plan is stuck when:
//   - plan.status == "EXECUTING"
//   - updated_at is older than staleDays
//   - queue shows 0 RUNNING tasks
//   - either: 0 QUEUED/DRAFT tasks (nothing to run), or all remaining
//     tasks are BLOCKED with unmet dependencies
func (s *Store) FindStuck(ctx context.Context, staleDays int, limit int) ([]StuckPlan, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -staleDays)

	candidates, err := s.plans.List(ctx, plans.ListOptions{
		Status: []plans.PlanStatus{StatusExecuting},
		Limit:  limit * 2,
	})
	if err != nil {
		return nil, fmt.Errorf("list executing plans: %w", err)
	}

	var stuck []StuckPlan
	for _, plan := range candidates {
		if len(stuck) >= limit {
			break
		}
		if plan.UpdatedAt.After(cutoff) {
			continue
		}

		tasks, err := s.tasks.ListTasks(ctx, taskqueue.ListTasksOptions{
			PlanID: plan.PlanID,
			Limit:  10000,
		})
		if err != nil {
			continue
		}

		var running, queued, failed, blocked, completed int
		for _, t := range tasks {
			switch t.Status {
			case taskqueue.TaskStatusRunning:
				running++
			case taskqueue.TaskStatusQueued, taskqueue.TaskStatusDraft:
				queued++
			case taskqueue.TaskStatusFailed:
				failed++
			case taskqueue.TaskStatusBlocked:
				blocked++
			case taskqueue.TaskStatusCompleted:
				completed++
			}
		}
		total := len(tasks)

		if running > 0 {
			continue
		}

		var reason string
		if queued == 0 && blocked == 0 {
			reason = "zero_running_zero_queued"
		} else if queued == 0 && blocked > 0 {
			reason = "all_remaining_blocked"
		} else if total == 0 {
			reason = "never_decomposed"
		} else {
			continue
		}

		stuck = append(stuck, StuckPlan{
			Plan:        &plan,
			StuckReason: reason,
			QueueSummary: QueueSummary{
				Total:     total,
				Completed: completed,
				Failed:    failed,
				Blocked:   blocked,
				Queued:    queued,
				Running:   running,
			},
			LastUpdated: plan.UpdatedAt,
		})
	}

	return stuck, nil
}

// ------------------------------------------------------------------
// FindUndecomposed — plans with zero tasks (port of planstore.py find_undecomposed_plans)
// ------------------------------------------------------------------

// UndecomposedPlan is a plan with task_count == 0.
type UndecomposedPlan struct {
	Plan      *Plan `json:"plan"`
	TaskCount int   `json:"task_count"`
}

// FindUndecomposed finds plans that have zero tasks in the queue (never decomposed).
func (s *Store) FindUndecomposed(ctx context.Context, limit int) ([]UndecomposedPlan, error) {
	allPlans, err := s.plans.List(ctx, plans.ListOptions{Limit: limit * 3})
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}

	var results []UndecomposedPlan
	for _, plan := range allPlans {
		if len(results) >= limit {
			break
		}
		count, err := s.tasks.ListTasks(ctx, taskqueue.ListTasksOptions{
			PlanID: plan.PlanID,
			Limit:  1,
		})
		if err != nil {
			continue
		}
		if len(count) == 0 {
			results = append(results, UndecomposedPlan{
				Plan:      &plan,
				TaskCount: 0,
			})
		}
	}

	return results, nil
}

// ------------------------------------------------------------------
// SyncProgress — rewrite Markdown from queue truth (port of planstore.py sync_plan_progress)
// ------------------------------------------------------------------

// SyncResult reports what SyncProgress changed.
type SyncResult struct {
	Synced    bool   `json:"synced"`
	PlanID    string `json:"plan_id"`
	Total     int    `json:"total,omitempty"`
	Completed int    `json:"completed,omitempty"`
	Failed    int    `json:"failed,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// SyncProgress syncs Markdown status badges and progress summary to queue
// truth. Rewrites `**Status:**` lines and `**Progress:**` summary in the
// plan content from actual queue state.
func (s *Store) SyncProgress(ctx context.Context, planID string) (*SyncResult, error) {
	plan, err := s.plans.Get(ctx, planID)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return &SyncResult{Synced: false, PlanID: planID, Reason: "plan not found"}, nil
		}
		return nil, err
	}

	tasks, err := s.tasks.ListTasks(ctx, taskqueue.ListTasksOptions{
		PlanID: planID,
		Limit:  10000,
	})
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}

	statusMap := make(map[string]string, len(tasks))
	for _, t := range tasks {
		statusMap[t.TaskID] = string(t.Status)
	}

	content := plan.Content
	if content == "" {
		return &SyncResult{Synced: false, PlanID: planID, Reason: "no content"}, nil
	}

	// Replace **Status:** PENDING|RUNNING|SUCCESS|FAILED|SKIPPED with queue truth
	statusReplRe := regexp.MustCompile(`(\*\*Status:\*\*\s*)(PENDING|RUNNING|SUCCESS|FAILED|SKIPPED)(\s*\n)`)
	newContent := statusReplRe.ReplaceAllStringFunc(content, func(match string) string {
		parts := statusReplRe.FindStringSubmatch(match)
		if parts == nil {
			return match
		}
		// Try to find the task ID from the surrounding context.
		// The task heading is usually just above: "### T01: ..."
		// We do a best-effort lookup: if the match is inside a task block,
		// find the nearest preceding "### T<digits>:" heading.
		taskID := s.inferTaskIDFromPosition(content, match)
		truth := statusMap[taskID]
		if truth == "" {
			truth = "PENDING"
		}
		return parts[1] + truth + parts[3]
	})

	// Update progress summary line if present
	total := len(tasks)
	completed := 0
	failed := 0
	for _, t := range tasks {
		if t.Status == taskqueue.TaskStatusCompleted {
			completed++
		}
		if t.Status == taskqueue.TaskStatusFailed {
			failed++
		}
	}
	newSummary := fmt.Sprintf("**Progress:** %d/%d completed, %d failed", completed, total, failed)
	progressRe := regexp.MustCompile(`\*\*Progress:\*\*\s*\d+/\d+[^\n]*`)
	newContent = progressRe.ReplaceAllString(newContent, newSummary)

	if newContent != content {
		_, err := s.plans.Update(ctx, planID, bson.M{"content": newContent})
		if err != nil {
			return nil, fmt.Errorf("update plan content: %w", err)
		}
		return &SyncResult{
			Synced:    true,
			PlanID:    planID,
			Total:     total,
			Completed: completed,
			Failed:    failed,
		}, nil
	}

	return &SyncResult{Synced: false, PlanID: planID, Reason: "no changes needed"}, nil
}

// inferTaskIDFromPosition does a best-effort scan backward from the match
// position to find the nearest "### T<digits>:" heading and returns the
// task ID. If none is found, returns "".
func (s *Store) inferTaskIDFromPosition(content, match string) string {
	idx := strings.Index(content, match)
	if idx < 0 {
		return ""
	}
	prefix := content[:idx]
	// Find last "### T" heading in prefix
	lines := strings.Split(prefix, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if m := taskHeadingRe.FindStringSubmatch(lines[i]); m != nil {
			return m[1]
		}
	}
	return ""
}

// ------------------------------------------------------------------
// Collection passthrough
// ------------------------------------------------------------------

// Name returns the underlying collection name.
func (s *Store) Name() string {
	return s.plans.Name()
}

// Database returns the underlying database.
func (s *Store) Database() *mongo.Database {
	return s.plans.Database()
}
