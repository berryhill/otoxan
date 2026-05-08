package dispatch

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// RequestStatus is the state of a dispatch request in the pipeline.
type RequestStatus string

const (
	RequestPending    RequestStatus = "PENDING"
	RequestClaimed    RequestStatus = "CLAIMED"
	RequestFulfilled  RequestStatus = "FULFILLED"
	RequestFailed     RequestStatus = "FAILED"
	RequestDropped    RequestStatus = "DROPPED"
	RequestCancelled  RequestStatus = "CANCELLED"
)

// DispatchRequest mirrors the dispatch_requests MongoDB collection.
// It bridges QUEUED tasks to the dispatch pipeline.
type DispatchRequest struct {
	RequestID   string              `bson:"request_id" json:"request_id"`
	TaskID      string              `bson:"task_id" json:"task_id"`
	Status      RequestStatus       `bson:"status" json:"status"`
	CreatedAt   time.Time           `bson:"created_at" json:"created_at"`
	ClaimedAt   *time.Time          `bson:"claimed_at,omitempty" json:"claimed_at,omitempty"`
	FulfilledAt *time.Time          `bson:"fulfilled_at,omitempty" json:"fulfilled_at,omitempty"`
	Priority    int                 `bson:"priority" json:"priority"`
	Error       string              `bson:"error,omitempty" json:"error,omitempty"`
	Extra       map[string]any      `bson:",inline,omitempty" json:"-"`
}

// SpawnStatus is the lifecycle state of a worker spawn.
type SpawnStatus string

const (
	SpawnRunning    SpawnStatus = "RUNNING"
	SpawnCompleted  SpawnStatus = "COMPLETED"
	SpawnFailed     SpawnStatus = "FAILED"
)

// SpawnRecord tracks an in-memory worker process.
type SpawnRecord struct {
	TaskID         string      `bson:"task_id" json:"task_id"`
	RequestID      string      `bson:"request_id" json:"request_id"`
	SessionID      string      `bson:"session_id" json:"session_id"`
	PID            int         `bson:"pid" json:"pid"`
	StartedAt      time.Time   `bson:"started_at" json:"started_at"`
	Status         SpawnStatus `bson:"status" json:"status"`
	ExitCode       int         `bson:"exit_code" json:"exit_code"`
	TaskStatus     string      `bson:"task_status" json:"task_status"`
	LogTail        []string    `bson:"log_tail,omitempty" json:"log_tail,omitempty"`
	RuntimeSeconds int         `bson:"runtime_seconds" json:"runtime_seconds"`
	ErrorSummary   string      `bson:"error_summary,omitempty" json:"error_summary,omitempty"`
	Lane           string      `bson:"lane" json:"lane"`
}

// Completion is the JSON marker written by a worker to /tmp/dispatch_completed/{task_id}.json.
type Completion struct {
	TaskID         string    `bson:"task_id" json:"task_id"`
	TaskStatus     string    `bson:"task_status" json:"task_status"`
	Output         string    `bson:"output" json:"output"`
	ExitCode       int       `bson:"exit_code" json:"exit_code"`
	RuntimeSeconds int       `bson:"runtime_seconds,omitempty" json:"runtime_seconds,omitempty"`
	ErrorSummary   string    `bson:"error_summary,omitempty" json:"error_summary,omitempty"`
	LastLogLines   []string  `bson:"last_log_lines,omitempty" json:"last_log_lines,omitempty"`
	SessionID      string    `bson:"session_id" json:"session_id"`
	CompletedAt    time.Time `bson:"completed_at" json:"completed_at"`
}

// SpawnRequest is sent from claimLoop to spawnSupervisor to fork a worker.
type SpawnRequest struct {
	TaskID      string
	RequestID   string
	SessionID   string
	Prompt      string
	Toolsets    []string
	AgentName   string
	Lane        string
	Workdir     string
	FlowSession *FlowSessionInfo
}

// SpawnResult is returned by spawnSupervisor to dispatchLoop after forking.
type SpawnResult struct {
	TaskID    string
	RequestID string
	SessionID string
	PID       int
	LogFile   string
	Error     string
	SpawnedAt time.Time
}

// FlowSessionInfo carries flow-path metadata when a task belongs to a flow.
type FlowSessionInfo struct {
	FlowID    string `json:"flow_id"`
	StepID    string `json:"step_id"`
	SessionID string `json:"session_id"`
}

// ReapSummary is sent from reapLoop to dispatchLoop after a reap pass.
type ReapSummary struct {
	ReapedCompleted  int
	ReapedFailed     int
	ReapedStale      int
	StillRunning     int
	OrphanTasksReset int
}

// ClaimSummary is sent from claimLoop to dispatchLoop after a claim pass.
type ClaimSummary struct {
	Dispatched []DispatchInfo
	Failed     []string // taskIDs that failed to claim
}

// DispatchInfo is the JSON status record printed by the dispatcher.
type DispatchInfo struct {
	Action       string          `json:"action"`
	TaskID       string          `json:"task_id"`
	RequestID    string          `json:"request_id"`
	SessionID    string          `json:"session_id"`
	PID          int             `json:"pid"`
	LogFile      string          `json:"log_file"`
	Toolsets     []string        `json:"toolsets"`
	Role         string          `json:"role"`
	PlanID       string          `json:"plan_id"`
	Title        string          `json:"title"`
	SpawnedAt    time.Time       `json:"spawned_at"`
	Agent        string          `json:"agent"`
	TeamID       string          `json:"team_id,omitempty"`
	InitiativeID string          `json:"initiative_id,omitempty"`
	UseFlowPath  bool            `json:"use_flow_path"`
	FlowSession  *FlowSessionInfo `json:"flow_session,omitempty"`
}

// Channel types for goroutine coordination.
type (
	// EnsureTick triggers ensureLoop to bridge QUEUED tasks → PENDING requests.
	EnsureTick struct{}

	// ReapTick triggers reapLoop to check completion markers and PID liveness.
	ReapTick struct{}

	// ReclaimTick triggers reclaimLoop to reset stale CLAIMED requests.
	ReclaimTick struct{}

	// SlotQuery is sent to slotCounter to ask for available slots.
	SlotQuery struct{}

	// SlotResp is the response from slotCounter with available concurrency slots.
	SlotResp struct {
		Available int
		Active    int
		Claimed   int
	}

	// ClaimSlots carries the number of slots claimLoop may fill.
	ClaimSlots struct {
		Slots int
	}

	// CleanupTask is sent to cleanupWorker to reset a task/request/spawn.
	CleanupTask struct {
		TaskID string
		Reason string
	}
)

// BSONRoundTrip is a test helper that marshals v to BSON and back into dst.
func BSONRoundTrip(v any, dst any) error {
	data, err := bson.Marshal(v)
	if err != nil {
		return err
	}
	return bson.Unmarshal(data, dst)
}
