package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/silas/otoxan/internal/auth"
	"github.com/silas/otoxan/internal/mcp"
	"github.com/silas/otoxan/internal/store/queue"
	"github.com/silas/otoxan/internal/store/tasks"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const version = "0.1.0"

func main() {
	healthCheck := flag.Bool("health-check", false, "Run health check (connect to Mongo and exit)")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle SIGINT/SIGTERM to cancel ctx.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	client, dbName, err := auth.MongoClient(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mongo init: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = client.Disconnect(ctx) }()

	// Health check mode: ping Mongo and exit
	if *healthCheck {
		if err := client.Ping(ctx, nil); err != nil {
			fmt.Fprintf(os.Stderr, "health-check ping: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	db := client.Database(dbName)
	taskStore := tasks.NewTaskStore(db.Collection("tasks"))
	taskQueue := queue.NewTaskQueue(db, taskStore)

	srv := mcp.New("otoxan-tasks", version)
	registerTools(srv, taskStore, taskQueue)

	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}

// ------------------------------------------------------------------
// Arg structs
// ------------------------------------------------------------------

type listTasksArgs struct {
	Agent          string   `json:"agent"`
	Status         []string `json:"status"`
	Limit          int      `json:"limit"`
	IncludeDeleted bool     `json:"include_deleted"`
}

type getTaskArgs struct {
	TaskID string `json:"task_id" jsonschema:"required"`
}

type createTaskArgs struct {
	TaskID       string   `json:"task_id" jsonschema:"required"`
	Title        string   `json:"title" jsonschema:"required"`
	Description  string   `json:"description"`
	Type         string   `json:"type"`
	Status       string   `json:"status"`
	Priority     int      `json:"priority"`
	Assignee     string   `json:"assignee"`
	AssigneeType string   `json:"assignee_type"`
	MaxRetries   int      `json:"max_retries"`
	Labels       []string `json:"labels"`
	DependsOn    []string `json:"depends_on"`
	Intent       string   `json:"intent"`
	Implementation string `json:"implementation"`
	References   string   `json:"references"`
	PlanGoal     string   `json:"plan_goal"`
	PlanContext  string   `json:"plan_context"`
	PhaseContext string   `json:"phase_context"`
}

type updateTaskArgs struct {
	TaskID      string `json:"task_id" jsonschema:"required"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Priority    int    `json:"priority"`
	Assignee    string `json:"assignee"`
	Output      string `json:"output"`
}

type deleteTaskArgs struct {
	TaskID string `json:"task_id" jsonschema:"required"`
}

type claimTaskArgs struct {
	Agent string `json:"agent" jsonschema:"required"`
}

type markCompletedArgs struct {
	TaskID string `json:"task_id" jsonschema:"required"`
	Output string `json:"output"`
}

type markFailedArgs struct {
	TaskID string `json:"task_id" jsonschema:"required"`
	Reason string `json:"reason"`
}

type markRetriedArgs struct {
	TaskID string `json:"task_id" jsonschema:"required"`
}

type queueStatusArgs struct{}

type getRunnableArgs struct {
	Agent string `json:"agent" jsonschema:"required"`
}

// ------------------------------------------------------------------
// Tool registration
// ------------------------------------------------------------------

func registerTools(srv *mcp.Server, store *tasks.TaskStore, tq *queue.TaskQueue) {
	srv.Register(mcp.Tool{
		Name:        "list_tasks",
		Description: "List tasks matching optional filters (agent, status, limit).",
		InputSchema: mcp.SchemaOf[listTasksArgs](),
		Handler:     handleListTasks(store),
	})
	srv.Register(mcp.Tool{
		Name:        "get_task",
		Description: "Retrieve a single task by its task_id.",
		InputSchema: mcp.SchemaOf[getTaskArgs](),
		Handler:     handleGetTask(store),
	})
	srv.Register(mcp.Tool{
		Name:        "create_task",
		Description: "Create a new task document.",
		InputSchema: mcp.SchemaOf[createTaskArgs](),
		Handler:     handleCreateTask(store),
	})
	srv.Register(mcp.Tool{
		Name:        "update_task",
		Description: "Patch fields of an existing task.",
		InputSchema: mcp.SchemaOf[updateTaskArgs](),
		Handler:     handleUpdateTask(store),
	})
	srv.Register(mcp.Tool{
		Name:        "delete_task",
		Description: "Soft-delete a task by task_id.",
		InputSchema: mcp.SchemaOf[deleteTaskArgs](),
		Handler:     handleDeleteTask(store),
	})
	srv.Register(mcp.Tool{
		Name:        "claim_task",
		Description: "Atomically claim the next available QUEUED task for an agent.",
		InputSchema: mcp.SchemaOf[claimTaskArgs](),
		Handler:     handleClaimTask(tq),
	})
	srv.Register(mcp.Tool{
		Name:        "mark_completed",
		Description: "Mark a RUNNING task as COMPLETED with optional output.",
		InputSchema: mcp.SchemaOf[markCompletedArgs](),
		Handler:     handleMarkCompleted(tq),
	})
	srv.Register(mcp.Tool{
		Name:        "mark_failed",
		Description: "Mark a RUNNING or CLAIMED task as FAILED with an optional reason.",
		InputSchema: mcp.SchemaOf[markFailedArgs](),
		Handler:     handleMarkFailed(tq),
	})
	srv.Register(mcp.Tool{
		Name:        "mark_retried",
		Description: "Transition a FAILED task back to QUEUED for retry.",
		InputSchema: mcp.SchemaOf[markRetriedArgs](),
		Handler:     handleMarkRetried(tq),
	})
	srv.Register(mcp.Tool{
		Name:        "queue_status",
		Description: "Return counts of tasks per status.",
		InputSchema: mcp.SchemaOf[queueStatusArgs](),
		Handler:     handleQueueStatus(tq),
	})
	srv.Register(mcp.Tool{
		Name:        "get_runnable",
		Description: "Claim the next runnable task for an agent (alias for claim_task).",
		InputSchema: mcp.SchemaOf[getRunnableArgs](),
		Handler:     handleGetRunnable(tq),
	})
}

// ------------------------------------------------------------------
// Handlers
// ------------------------------------------------------------------

func handleListTasks(store *tasks.TaskStore) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args listTasksArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		statuses := make([]tasks.TaskStatus, len(args.Status))
		for i, s := range args.Status {
			statuses[i] = tasks.TaskStatus(s)
		}
		opts := tasks.ListOptions{
			Assignee:       args.Agent,
			Status:         statuses,
			Limit:          args.Limit,
			IncludeDeleted: args.IncludeDeleted,
		}
		result, err := store.List(ctx, opts)
		if err != nil {
			return nil, fmt.Errorf("store list: %w", err)
		}
		return result, nil
	}
}

func handleGetTask(store *tasks.TaskStore) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args getTaskArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.TaskID == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "task_id is required"}
		}
		task, err := store.Get(ctx, args.TaskID)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "task not found: " + args.TaskID}
			}
			return nil, fmt.Errorf("store get: %w", err)
		}
		return task, nil
	}
}

func handleCreateTask(store *tasks.TaskStore) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args createTaskArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.TaskID == "" || args.Title == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "task_id and title are required"}
		}
		t := &tasks.Task{
			TaskID:         args.TaskID,
			Title:          args.Title,
			Description:    args.Description,
			Priority:       args.Priority,
			Assignee:       args.Assignee,
			AssigneeType:   args.AssigneeType,
			MaxRetries:     args.MaxRetries,
			Labels:         args.Labels,
			DependsOn:      args.DependsOn,
			Intent:         args.Intent,
			Implementation: args.Implementation,
			References:     args.References,
			PlanGoal:       args.PlanGoal,
			PlanContext:    args.PlanContext,
			PhaseContext:   args.PhaseContext,
		}
		if args.Type != "" {
			t.Type = tasks.TaskType(args.Type)
		}
		if args.Status != "" {
			t.Status = tasks.TaskStatus(args.Status)
		}
		_, err := store.Create(ctx, t)
		if err != nil {
			return nil, fmt.Errorf("store create: %w", err)
		}
		return t, nil
	}
}

func handleUpdateTask(store *tasks.TaskStore) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args updateTaskArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.TaskID == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "task_id is required"}
		}
		updates := bson.M{}
		if args.Title != "" {
			updates["title"] = args.Title
		}
		if args.Description != "" {
			updates["description"] = args.Description
		}
		if args.Status != "" {
			updates["status"] = args.Status
		}
		if args.Priority != 0 {
			updates["priority"] = args.Priority
		}
		if args.Assignee != "" {
			updates["assignee"] = args.Assignee
		}
		if args.Output != "" {
			updates["output"] = args.Output
		}
		if len(updates) == 0 {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "no fields provided to update"}
		}
		_, err := store.Update(ctx, args.TaskID, updates)
		if err != nil {
			return nil, fmt.Errorf("store update: %w", err)
		}
		task, err := store.Get(ctx, args.TaskID)
		if err != nil {
			return nil, fmt.Errorf("store get after update: %w", err)
		}
		return task, nil
	}
}

func handleDeleteTask(store *tasks.TaskStore) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args deleteTaskArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.TaskID == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "task_id is required"}
		}
		_, err := store.Delete(ctx, args.TaskID)
		if err != nil {
			return nil, fmt.Errorf("store delete: %w", err)
		}
		return map[string]string{"deleted": args.TaskID}, nil
	}
}

func handleClaimTask(tq *queue.TaskQueue) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args claimTaskArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.Agent == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "agent is required"}
		}
		task, err := tq.Claim(ctx, args.Agent)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "no queued tasks available"}
			}
			return nil, fmt.Errorf("queue claim: %w", err)
		}
		return task, nil
	}
}

func handleMarkCompleted(tq *queue.TaskQueue) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args markCompletedArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.TaskID == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "task_id is required"}
		}
		task, err := tq.MarkCompleted(ctx, args.TaskID, args.Output)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "task not found or not in RUNNING state: " + args.TaskID}
			}
			return nil, fmt.Errorf("queue mark completed: %w", err)
		}
		return task, nil
	}
}

func handleMarkFailed(tq *queue.TaskQueue) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args markFailedArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.TaskID == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "task_id is required"}
		}
		task, err := tq.MarkFailed(ctx, args.TaskID, args.Reason)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "task not found or not in RUNNING/CLAIMED state: " + args.TaskID}
			}
			return nil, fmt.Errorf("queue mark failed: %w", err)
		}
		return task, nil
	}
}

func handleMarkRetried(tq *queue.TaskQueue) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args markRetriedArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.TaskID == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "task_id is required"}
		}
		task, err := tq.MarkRetried(ctx, args.TaskID)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "task not found or not in FAILED state: " + args.TaskID}
			}
			return nil, fmt.Errorf("queue mark retried: %w", err)
		}
		return task, nil
	}
}

func handleQueueStatus(tq *queue.TaskQueue) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args queueStatusArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		counts, err := tq.QueueStatus(ctx)
		if err != nil {
			return nil, fmt.Errorf("queue status: %w", err)
		}
		return counts, nil
	}
}

func handleGetRunnable(tq *queue.TaskQueue) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args getRunnableArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.Agent == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "agent is required"}
		}
		task, err := tq.Claim(ctx, args.Agent)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "no runnable tasks available"}
			}
			return nil, fmt.Errorf("queue get runnable: %w", err)
		}
		return task, nil
	}
}
