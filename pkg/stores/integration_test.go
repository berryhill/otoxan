//go:build integration

// Package stores_integration_test provides an end-to-end integration test
// harness that exercises all pkg/stores packages together against a live
// testcontainers MongoDB instance.
//
// The test flow is:
//   1. Register an agent in the global registry
//   2. Create a plan for that agent
//   3. Decompose the plan into tasks
//   4. Claim a task
//   5. Mark the task running and then completed
//   6. Verify events were recorded
//
// Run with: go test -tags=integration ./pkg/stores/...
package stores_integration_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/silas/otoxan/pkg/stores/agentregistry"
	"github.com/silas/otoxan/pkg/stores/eventstore"
	"github.com/silas/otoxan/pkg/stores/planstore"
	"github.com/silas/otoxan/pkg/stores/taskqueue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// setupMongo spins up a testcontainers MongoDB container and returns a client.
func setupMongo(t *testing.T) *mongo.Client {
	t.Helper()
	ctx := context.Background()

	container, err := mongodb.Run(ctx, "mongo:7")
	if err != nil {
		t.Fatalf("failed to start mongodb container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(ctx)
	})

	uri, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}
	os.Setenv("MONGO_URI", uri)

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("failed to connect to mongodb: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Disconnect(ctx)
	})

	return client
}

// ------------------------------------------------------------------
// End-to-end integration test
// ------------------------------------------------------------------

func TestEndToEnd_RegisterPlanDecomposeClaimCompleteEvents(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)

	agentName := "integration-agent"

	// ----------------------------------------------------------------
	// 1. Register agent in global registry
	// ----------------------------------------------------------------
	regStore, err := agentregistry.NewStore(client)
	require.NoError(t, err)

	res, err := regStore.Register(ctx, agentName, "backend")
	require.NoError(t, err)
	require.NotNil(t, res.InsertedID)

	// Verify agent exists in global DB
	agent, err := regStore.Get(ctx, agentName)
	require.NoError(t, err)
	assert.Equal(t, agentName, agent.Name)
	assert.Equal(t, "backend", agent.Role)
	assert.Equal(t, fmt.Sprintf("otoxan_agent_%s", agentName), agent.DBName)
	assert.Equal(t, agentregistry.AgentStatusActive, agent.Status)

	// Verify per-agent DB was created
	agentDB := client.Database(agent.DBName)
	colls, err := agentDB.ListCollectionNames(ctx, bson.M{})
	require.NoError(t, err)
	assert.Contains(t, colls, "__init")

	// ----------------------------------------------------------------
	// 2. Create a plan for the agent
	// ----------------------------------------------------------------
	ps, err := planstore.NewStore(client, agentName)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Millisecond)
	plan := &planstore.Plan{
		PlanID:    "plan_e2e_001",
		Title:     "Integration Test Plan",
		Status:    planstore.StatusExecuting,
		Owner:     agentName,
		CreatedAt: now,
		UpdatedAt: now,
		Content: `# Integration Test Plan

### T1: Set up infrastructure
**Status:** PENDING
**Depends on:** (none)
**Assigned:** integration_agent

### T2: Implement core logic
**Status:** PENDING
**Depends on:** T1
**Assigned:** integration_agent

### T3: Verify end-to-end
**Status:** PENDING
**Depends on:** T2
**Assigned:** integration_agent
`,
		Tags:     []string{},
		PlanType: planstore.TypeStandard,
	}

	planRes, err := ps.Create(ctx, plan)
	require.NoError(t, err)
	require.NotNil(t, planRes.InsertedID)

	// Verify plan was created
	gotPlan, err := ps.Get(ctx, "plan_e2e_001")
	require.NoError(t, err)
	assert.Equal(t, "plan_e2e_001", gotPlan.PlanID)
	assert.Equal(t, "Integration Test Plan", gotPlan.Title)
	assert.Equal(t, planstore.StatusExecuting, gotPlan.Status)

	// ----------------------------------------------------------------
	// 3. Decompose plan into tasks (using BulkCreateTasks)
	// ----------------------------------------------------------------
	tq, err := taskqueue.NewStore(client, agentName)
	require.NoError(t, err)

	tasksData := []bson.M{
		{
			"title":       "Set up infrastructure",
			"description": "Provision testcontainers and indexes",
			"plan_id":     "plan_e2e_001",
			"status":      string(taskqueue.TaskStatusQueued),
			"priority":    1,
			"assignee":    agentName,
			"assignee_type": "agent",
		},
		{
			"title":       "Implement core logic",
			"description": "Write the business logic layer",
			"plan_id":     "plan_e2e_001",
			"status":      string(taskqueue.TaskStatusQueued),
			"priority":    2,
			"assignee":    agentName,
			"assignee_type": "agent",
			"depends_on":  []string{}, // will be set after first task created
		},
		{
			"title":       "Verify end-to-end",
			"description": "Run the full integration test",
			"plan_id":     "plan_e2e_001",
			"status":      string(taskqueue.TaskStatusQueued),
			"priority":    3,
			"assignee":    agentName,
			"assignee_type": "agent",
			"depends_on":  []string{}, // will be set after second task created
		},
	}

	taskIDs, err := tq.BulkCreateTasks(ctx, tasksData)
	require.NoError(t, err)
	require.Len(t, taskIDs, 3)

	// Update dependencies: T2 depends on T1, T3 depends on T2
	_, err = tq.UpdateTask(ctx, taskIDs[1], bson.M{"depends_on": []string{taskIDs[0]}})
	require.NoError(t, err)
	_, err = tq.UpdateTask(ctx, taskIDs[2], bson.M{"depends_on": []string{taskIDs[1]}})
	require.NoError(t, err)

	// Verify tasks exist
	for i, tid := range taskIDs {
		task, err := tq.GetTask(ctx, tid)
		require.NoError(t, err)
		assert.Equal(t, tid, task.TaskID)
		switch i {
		case 0:
			assert.Equal(t, "Set up infrastructure", task.Title)
			assert.Empty(t, task.DependsOn)
		case 1:
			assert.Equal(t, "Implement core logic", task.Title)
			assert.Equal(t, []string{taskIDs[0]}, task.DependsOn)
		case 2:
			assert.Equal(t, "Verify end-to-end", task.Title)
			assert.Equal(t, []string{taskIDs[1]}, task.DependsOn)
		}
	}

	// Verify plan now has tasks (undecomposed check should be empty)
	undecomposed, err := ps.FindUndecomposed(ctx, 10)
	require.NoError(t, err)
	for _, u := range undecomposed {
		assert.NotEqual(t, "plan_e2e_001", u.Plan.PlanID)
	}

	// ----------------------------------------------------------------
	// 4. Claim the first task (T1 — no dependencies)
	// ----------------------------------------------------------------
	claimed, err := tq.ClaimTask(ctx, agentName, 5)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, taskIDs[0], claimed.TaskID)
	assert.Equal(t, "Set up infrastructure", claimed.Title)
	assert.Equal(t, taskqueue.TaskStatusClaimed, claimed.Status)
	assert.Equal(t, agentName, claimed.Assignee)
	assert.NotNil(t, claimed.ClaimedAt)

	// ----------------------------------------------------------------
	// 5. Mark running, then completed
	// ----------------------------------------------------------------
	ok, err := tq.MarkRunning(ctx, taskIDs[0], "sess_e2e_001")
	require.NoError(t, err)
	assert.True(t, ok)

	runningTask, err := tq.GetTask(ctx, taskIDs[0])
	require.NoError(t, err)
	assert.Equal(t, taskqueue.TaskStatusRunning, runningTask.Status)
	assert.Equal(t, 1, runningTask.Attempts)
	assert.NotNil(t, runningTask.StartedAt)

	// Mark completed with output and artifacts
	arts := []taskqueue.Artifact{
		{Name: "log", Type: "text", URL: "/tmp/e2e.log"},
		{Name: "report", Type: "json", URL: "/tmp/e2e_report.json"},
	}
	ok, err = tq.MarkCompleted(ctx, taskIDs[0], "Infrastructure provisioned successfully", arts)
	require.NoError(t, err)
	assert.True(t, ok)

	completedTask, err := tq.GetTask(ctx, taskIDs[0])
	require.NoError(t, err)
	assert.Equal(t, taskqueue.TaskStatusCompleted, completedTask.Status)
	assert.Equal(t, "Infrastructure provisioned successfully", *completedTask.Output)
	assert.Len(t, completedTask.Artifacts, 2)
	assert.NotNil(t, completedTask.CompletedAt)

	// ----------------------------------------------------------------
	// 6. Verify events are present for the completed task
	// ----------------------------------------------------------------
	events, err := tq.GetEvents(ctx, taskIDs[0], 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(events), 4, "expected at least task_created, task_queued, task_started, task_completed events")

	// Verify event types
	eventTypes := make([]taskqueue.EventType, len(events))
	for i, e := range events {
		eventTypes[i] = e.EventType
	}
	assert.Contains(t, eventTypes, taskqueue.EventTypeTaskCreated)
	assert.Contains(t, eventTypes, taskqueue.EventTypeTaskQueued)
	assert.Contains(t, eventTypes, taskqueue.EventTypeTaskStarted)
	assert.Contains(t, eventTypes, taskqueue.EventTypeTaskCompleted)
	assert.Contains(t, eventTypes, taskqueue.EventTypeTaskOutput)

	// Verify the task_completed event has duration data
	var completedEvent *taskqueue.TaskEventDoc
	for _, e := range events {
		if e.EventType == taskqueue.EventTypeTaskCompleted {
			completedEvent = &e
			break
		}
	}
	require.NotNil(t, completedEvent)
	assert.Equal(t, agentName, completedEvent.Actor)
	assert.NotNil(t, completedEvent.Data["final_status"])
	assert.NotNil(t, completedEvent.Data["duration_seconds"])

	// ----------------------------------------------------------------
	// 7. Claim the second task (T2 — now that T1 is completed)
	// ----------------------------------------------------------------
	claimed2, err := tq.ClaimTask(ctx, agentName, 5)
	require.NoError(t, err)
	require.NotNil(t, claimed2)
	assert.Equal(t, taskIDs[1], claimed2.TaskID)
	assert.Equal(t, "Implement core logic", claimed2.Title)

	// Mark T2 running and completed
	ok, err = tq.MarkRunning(ctx, taskIDs[1], "sess_e2e_002")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = tq.MarkCompleted(ctx, taskIDs[1], "Core logic implemented", nil)
	require.NoError(t, err)
	assert.True(t, ok)

	// Now T3 is claimable
	claimed3, err := tq.ClaimTask(ctx, agentName, 5)
	require.NoError(t, err)
	require.NotNil(t, claimed3)
	assert.Equal(t, taskIDs[2], claimed3.TaskID)

	// Mark T3 running so it's counted as RUNNING
	ok, err = tq.MarkRunning(ctx, taskIDs[2], "sess_e2e_003")
	require.NoError(t, err)
	assert.True(t, ok)

	// ----------------------------------------------------------------
	// 8. Verify execution status on the plan
	// ----------------------------------------------------------------
	execStatus, err := ps.GetExecutionStatus(ctx, "plan_e2e_001")
	require.NoError(t, err)
	assert.Equal(t, "plan_e2e_001", execStatus.PlanID)
	assert.Equal(t, 3, execStatus.Total)
	assert.Equal(t, 2, execStatus.Completed)
	assert.Equal(t, 0, execStatus.Failed)
	assert.Equal(t, 1, execStatus.Running) // T3 is running
	assert.Equal(t, 0, execStatus.Queued)
	assert.InDelta(t, 66.67, execStatus.PercentDone, 0.1)
	assert.False(t, execStatus.IsStuck)

	// ----------------------------------------------------------------
	// 9. Verify global audit events can be written and read
	// ----------------------------------------------------------------
	auditScope := eventstore.GlobalAuditEvents(client)
	auditStore, err := eventstore.NewStore(auditScope)
	require.NoError(t, err)

	_, err = auditStore.Append(ctx, eventstore.EventDoc{
		Type:   "agent_registered",
		Actor:  "system",
		Data:   bson.M{"agent_name": agentName, "role": "backend"},
	})
	require.NoError(t, err)

	_, err = auditStore.Append(ctx, eventstore.EventDoc{
		Type:   "plan_created",
		Actor:  agentName,
		Data:   bson.M{"plan_id": "plan_e2e_001", "title": "Integration Test Plan"},
	})
	require.NoError(t, err)

	// Tail audit events
	auditEvents, err := auditStore.Tail(ctx, eventstore.TailOptions{Limit: 10})
	require.NoError(t, err)
	require.Len(t, auditEvents, 2)
	assert.Equal(t, "plan_created", auditEvents[0].Type) // newest first
	assert.Equal(t, "agent_registered", auditEvents[1].Type)

	// Query by type
	agentEvents, err := auditStore.QueryByType(ctx, eventstore.QueryByTypeOptions{Type: "agent_registered"})
	require.NoError(t, err)
	require.Len(t, agentEvents, 1)
	assert.Equal(t, "system", agentEvents[0].Actor)

	// ----------------------------------------------------------------
	// 10. Verify per-agent task_events isolation
	// ----------------------------------------------------------------
	agentEventScope, err := eventstore.AgentTaskEvents(client, agentName)
	require.NoError(t, err)
	agentEventStore, err := eventstore.NewStore(agentEventScope)
	require.NoError(t, err)

	// The taskqueue already wrote events to the agent's task_events collection.
	// Verify we can read them via the eventstore scoped to the same collection.
	var perAgentEvents []eventstore.EventDoc
	perAgentEvents, err = agentEventStore.Tail(ctx, eventstore.TailOptions{Limit: 10})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(perAgentEvents), 1)
	// The most recent event should be from the task queue (task_claimed or later)
	assert.Equal(t, agentName, perAgentEvents[0].Actor)

	// ----------------------------------------------------------------
	// 11. Clean up — soft-delete agent and plan
	// ----------------------------------------------------------------
	_, err = ps.Delete(ctx, "plan_e2e_001")
	require.NoError(t, err)

	_, err = regStore.Delete(ctx, agentName)
	require.NoError(t, err)

	// Verify soft-delete worked
	_, err = ps.Get(ctx, "plan_e2e_001")
	assert.Equal(t, mongo.ErrNoDocuments, err)

	_, err = regStore.Get(ctx, agentName)
	assert.Equal(t, mongo.ErrNoDocuments, err)
}
