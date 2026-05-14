// Package plans provides a MongoDB-backed store for plan documents with
// soft-delete semantics.
package plans

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/silas/otoxan/internal/softdelete"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// NewPlanStore creates a PlanStore backed by the given MongoDB collection.
// It ensures required indexes on the plans collection.
func NewPlanStore(coll *mongo.Collection) *PlanStore {
	sd := softdelete.NewSoftDelete(coll)
	ps := &PlanStore{sd: sd}
	_ = ps.ensureIndexes(context.Background())
	return ps
}

// PlanStore is a MongoDB-backed store for plan documents with soft-delete
// semantics. It mirrors the Python planstore.PlanStore CRUD surface.
type PlanStore struct {
	sd *softdelete.SoftDeleteCollection
}

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (s *PlanStore) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "plan_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "updated_at", Value: 1}}},
		{Keys: bson.D{{Key: "archived_at", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "initiative_id", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "directive_id", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "team_id", Value: 1}}, Options: options.Index().SetSparse(true)},
		{Keys: bson.D{{Key: "flow_session_id", Value: 1}}, Options: options.Index().SetSparse(true)},
	}
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// ------------------------------------------------------------------
// CRUD
// ------------------------------------------------------------------

// Create inserts a new plan document. The caller must set PlanID and Title.
// Defaults are applied for optional fields.
func (s *PlanStore) Create(ctx context.Context, plan *Plan) (*mongo.InsertOneResult, error) {
	now := time.Now().UTC()
	if plan.CreatedAt.IsZero() {
		plan.CreatedAt = now
	}
	if plan.UpdatedAt.IsZero() {
		plan.UpdatedAt = now
	}
	if plan.Status == "" {
		plan.Status = StatusPlanning
	}
	if plan.PlanType == "" {
		plan.PlanType = TypeStandard
	}
	if plan.Tags == nil {
		plan.Tags = []string{}
	}
	return s.sd.InsertOne(ctx, plan)
}

// Get retrieves a single plan by plan_id. Returns mongo.ErrNoDocuments if
// not found (or soft-deleted).
func (s *PlanStore) Get(ctx context.Context, planID string) (*Plan, error) {
	sr := s.sd.FindOne(ctx, bson.M{"plan_id": planID})
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var p Plan
	if err := sr.Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// GetWithDeleted retrieves a plan including soft-deleted ones.
func (s *PlanStore) GetWithDeleted(ctx context.Context, planID string) (*Plan, error) {
	sr := s.sd.FindOne(ctx, bson.M{"plan_id": planID}, softdelete.WithIncludeDeleted())
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var p Plan
	if err := sr.Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Update patches fields of an existing plan. Sets updated_at automatically.
func (s *PlanStore) Update(ctx context.Context, planID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"plan_id": planID}, bson.M{"$set": updates})
}

// Delete soft-deletes a plan by plan_id.
func (s *PlanStore) Delete(ctx context.Context, planID string) (*mongo.UpdateResult, error) {
	return s.sd.Delete(ctx, bson.M{"plan_id": planID})
}

// Restore un-deletes a soft-deleted plan.
func (s *PlanStore) Restore(ctx context.Context, planID string) (*mongo.UpdateResult, error) {
	return s.sd.Restore(ctx, bson.M{"plan_id": planID})
}

// HardDelete permanently removes a plan.
func (s *PlanStore) HardDelete(ctx context.Context, planID string) (*mongo.DeleteResult, error) {
	return s.sd.HardDelete(ctx, bson.M{"plan_id": planID})
}

// ------------------------------------------------------------------
// List / Query
// ------------------------------------------------------------------

// ListOptions configures the List query.
type ListOptions struct {
	Status          []PlanStatus
	Tag             string
	Limit           int
	IncludeDeleted  bool
	IncludeArchived bool
}

// ListByStatus returns plans filtered by a single status, sorted by updated_at descending.
// This is a convenience wrapper around List for the common single-status case.
func (s *PlanStore) ListByStatus(ctx context.Context, status PlanStatus, limit int) ([]Plan, error) {
	return s.List(ctx, ListOptions{Status: []PlanStatus{status}, Limit: limit})
}

// List returns plans matching the provided filters, sorted by updated_at descending.
func (s *PlanStore) List(ctx context.Context, opts ListOptions) ([]Plan, error) {
	filter := bson.M{}
	if len(opts.Status) > 0 {
		if len(opts.Status) == 1 {
			filter["status"] = opts.Status[0]
		} else {
			statuses := make([]string, len(opts.Status))
			for i, st := range opts.Status {
				statuses[i] = string(st)
			}
			filter["status"] = bson.M{"$in": statuses}
		}
	}
	if opts.Tag != "" {
		filter["tags"] = opts.Tag
	}
	if !opts.IncludeArchived {
		filter["archived_at"] = bson.M{"$eq": nil}
	}

	var sdOpts []softdelete.Option
	if opts.IncludeDeleted {
		sdOpts = append(sdOpts, softdelete.WithIncludeDeleted())
	}

	cur, err := s.sd.Find(ctx, filter, sdOpts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var plans []Plan
	if err := cur.All(ctx, &plans); err != nil {
		return nil, err
	}

	// Sort by updated_at desc, limit in-memory
	sortPlans(plans)
	if opts.Limit > 0 && len(plans) > opts.Limit {
		plans = plans[:opts.Limit]
	}
	return plans, nil
}

// Count returns the number of plans matching the filter.
func (s *PlanStore) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.sd.CountDocuments(ctx, filter)
}

// Archive sets archived_at on a plan.
func (s *PlanStore) Archive(ctx context.Context, planID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"plan_id": planID}, bson.M{"$set": bson.M{"archived_at": now, "updated_at": now}})
}

// Unarchive clears archived_at on a plan.
func (s *PlanStore) Unarchive(ctx context.Context, planID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"plan_id": planID}, bson.M{"$set": bson.M{"archived_at": nil, "updated_at": now}})
}

// ------------------------------------------------------------------
// Task-queue helpers (stubs — full implementation needs task store)
// ------------------------------------------------------------------

// ExecutionStatus holds the ground-truth queue counts for a plan.
type ExecutionStatus struct {
	PlanID       string  `json:"plan_id"`
	Total        int     `json:"total"`
	Completed    int     `json:"completed"`
	Failed       int     `json:"failed"`
	Running      int     `json:"running"`
	Queued       int     `json:"queued"`
	Blocked      int     `json:"blocked"`
	Skipped      int     `json:"skipped"`
	Cancelled    int     `json:"cancelled"`
	Pending      int     `json:"pending"`
	PercentDone  float64 `json:"percent_done"`
	IsStuck      bool    `json:"is_stuck"`
	StuckReason  string  `json:"stuck_reason,omitempty"`
}

// GetExecutionStatus returns execution status for a plan by querying the task
// queue.  This is a stub; the real implementation needs a task-store reference.
func (s *PlanStore) GetExecutionStatus(ctx context.Context, planID string) (*ExecutionStatus, error) {
	// TODO: wire to task store once taskstore package is available
	return &ExecutionStatus{PlanID: planID}, nil
}

// SyncProgress rewrites Markdown status badges and progress summary from queue
// truth.  This is a stub; the real implementation needs a task-store reference.
func (s *PlanStore) SyncProgress(ctx context.Context, planID string) (map[string]interface{}, error) {
	// TODO: wire to task store once taskstore package is available
	return map[string]interface{}{"synced": false, "plan_id": planID, "reason": "not implemented"}, nil
}

// StuckPlan is a plan enriched with stuck-reason fields.
type StuckPlan struct {
	Plan
	StuckReason   string                 `json:"stuck_reason"`
	QueueSummary  map[string]interface{} `json:"queue_summary"`
	LastUpdated   time.Time              `json:"last_updated"`
}

// FindStuck finds plans that appear stuck in EXECUTING status.
// This is a stub; the real implementation needs a task-store reference.
func (s *PlanStore) FindStuck(ctx context.Context, staleDays int, limit int) ([]StuckPlan, error) {
	// TODO: wire to task store once taskstore package is available
	return nil, nil
}

// UndecomposedPlan is a plan that has zero tasks in the queue.
type UndecomposedPlan struct {
	Plan
	TaskCount int `json:"task_count"`
}

// FindUndecomposed finds plans that have zero tasks in the queue (never decomposed).
// This is a stub; the real implementation needs a task-store reference.
func (s *PlanStore) FindUndecomposed(ctx context.Context, limit int) ([]UndecomposedPlan, error) {
	// TODO: wire to task store once taskstore package is available
	return nil, nil
}

// ExtractedTask holds structured task data parsed from Markdown content.
type ExtractedTask struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Status         string   `json:"status"`
	DependsOn      []string `json:"depends_on"`
	Assigned       string   `json:"assigned,omitempty"`
	Tool           string   `json:"tool,omitempty"`
	Verify         string   `json:"verify,omitempty"`
	ParentProvider string   `json:"parent_provider,omitempty"`
}

// ExtractTasks extracts structured task data from Markdown plan content.
// Matches the Python planstore.extract_tasks static method.
func ExtractTasks(content string) []ExtractedTask {
	var tasks []ExtractedTask
	// Split on task-heading boundaries, keeping the delimiter
	parts := taskHeadingSplit(content)
	for _, part := range parts {
		m := taskHeadingMatch(part)
		if m == nil {
			continue
		}
		taskID := m[0]
		title := strings.ReplaceAll(strings.TrimSpace(m[1]), "\n", " ")
		endIdx, _ := strconv.Atoi(m[2])
		body := part[endIdx:]

		status := "PENDING"
		if sm := statusRegex.FindStringSubmatch(body); sm != nil {
			status = sm[1]
		}

		var dependsOn []string
		if dm := dependsRegex.FindStringSubmatch(body); dm != nil {
			depsStr := strings.TrimSpace(dm[1])
			if depsStr != "(none)" {
				dependsOn = splitAndTrim(depsStr, ",")
			}
		}

		assigned := ""
		if am := assignedRegex.FindStringSubmatch(body); am != nil {
			assigned = strings.TrimSpace(am[1])
		}

		tool := ""
		if tm := toolRegex.FindStringSubmatch(body); tm != nil {
			tool = strings.TrimSpace(tm[1])
		}

		verify := ""
		if vm := verifyRegex.FindStringSubmatch(body); vm != nil {
			verify = strings.TrimSpace(vm[1])
		}

		parentProvider := ""
		if pm := parentProviderRegex.FindStringSubmatch(body); pm != nil {
			parentProvider = strings.TrimSpace(pm[1])
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
// Regex helpers for ExtractTasks
// ------------------------------------------------------------------

var (
	statusRegex         = regexp.MustCompile(`\*\*Status:\*\*\s*(PENDING|RUNNING|SUCCESS|FAILED|SKIPPED)`)
	dependsRegex        = regexp.MustCompile(`\*\*Depends on:\*\*\s*(.+)`)
	assignedRegex       = regexp.MustCompile(`\*\*Assigned:\*\*\s*(.+)`)
	toolRegex           = regexp.MustCompile(`\*\*Tool:\*\*\s*(.+)`)
	verifyRegex         = regexp.MustCompile(`\*\*Verify:\*\*\s*(.+)`)
	parentProviderRegex = regexp.MustCompile(`\*\*Parent Provider:\*\*\s*(.+)`)
	_taskHeadingRe      = regexp.MustCompile(`(?m)^###\s+(T\d+):\s+(.*?)\n`)
)

func taskHeadingSplit(content string) []string {
	// Split on "### T<digits>:" boundaries, keeping the delimiter as part of each part.
	// Go regexp does not support (?=...), so we find all indices and split manually.
	re := regexp.MustCompile(`###\s+T\d+:\s`)
	matches := re.FindAllStringIndex(content, -1)
	if len(matches) == 0 {
		if strings.TrimSpace(content) != "" {
			return []string{content}
		}
		return nil
	}
	var out []string
	for i, m := range matches {
		start := m[0]
		end := len(content)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		out = append(out, content[start:end])
	}
	return out
}

func taskHeadingMatch(part string) []string {
	m := _taskHeadingRe.FindStringSubmatchIndex(part)
	if m == nil {
		return nil
	}
	// m[0],m[1] = full match; m[2],m[3] = group 1; m[4],m[5] = group 2
	return []string{
		part[m[2]:m[3]],      // taskID
		part[m[4]:m[5]],      // title
		strconv.Itoa(m[1]),   // end index of full match
	}
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// sortPlans sorts by updated_at descending.
func sortPlans(plans []Plan) {
	for i := 0; i < len(plans); i++ {
		for j := i + 1; j < len(plans); j++ {
			if plans[j].UpdatedAt.After(plans[i].UpdatedAt) {
				plans[i], plans[j] = plans[j], plans[i]
			}
		}
	}
}

// ------------------------------------------------------------------
// Collection passthrough
// ------------------------------------------------------------------

// Name returns the underlying collection name.
func (s *PlanStore) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *PlanStore) Database() *mongo.Database {
	return s.sd.Database()
}
