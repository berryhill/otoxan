package dispatch

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/silas/otoxan/internal/firstrun"
	"github.com/silas/otoxan/internal/sessionflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ------------------------------------------------------------------
// Fake flow for tests
// ------------------------------------------------------------------

// fakeFlow is a test SessionFlow that returns a fixed action/args based on input.
type fakeFlow struct {
	action  string
	payload string
}

func (f fakeFlow) Route(_ context.Context, in sessionflow.RouteInput) (sessionflow.RouteOutput, error) {
	return sessionflow.RouteOutput{
		Action:  f.action,
		Payload: f.payload,
	}, nil
}

// noopFlow echoes the input as a noop action.
type noopFlow struct{}

func (noopFlow) Route(_ context.Context, in sessionflow.RouteInput) (sessionflow.RouteOutput, error) {
	return sessionflow.RouteOutput{
		Action:  "noop",
		Payload: in.UserMessage,
	}, nil
}

// ------------------------------------------------------------------
// Test: runTurn with per-turn commit
// ------------------------------------------------------------------

func TestSession_runTurn_commit(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	// Seed a session.
	_, err := db.Exec(`INSERT INTO sessions (session_id, agent_id, flow_id, status, created_at, updated_at)
	                   VALUES ('sess-001', 'agent-a', 'default', 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	reg := sessionflow.NewRegistry()
	reg.Register("default", noopFlow{})

	// Override default registry temporarily.
	oldReg := sessionflow.DefaultRegistry
	sessionflow.DefaultRegistry = reg
	defer func() { sessionflow.DefaultRegistry = oldReg }()

	orch := NewOrchestrator(OrchestratorConfig{Default: "test", Models: map[string]ModelConfig{}})

	var out strings.Builder
	s := &Session{
		SessionID: "sess-001",
		AgentID:   "agent-a",
		FlowID:    "default",
		TaskID:    "task-001",
		db:        db,
		flow:      noopFlow{},
		orch:      orch,
		in:        strings.NewReader(""),
		out:       &out,
		step:      "",
	}

	handoff, err := s.runTurn(context.Background(), "hello")
	require.NoError(t, err)
	require.Nil(t, handoff)

	// Verify both turns are in the DB.
	rows, err := db.Query(`SELECT role, body FROM turns WHERE session_id = ? ORDER BY created_at`, "sess-001")
	require.NoError(t, err)
	defer rows.Close()

	var turns [][2]string
	for rows.Next() {
		var role, body string
		require.NoError(t, rows.Scan(&role, &body))
		turns = append(turns, [2]string{role, body})
	}
	require.NoError(t, rows.Err())

	require.Len(t, turns, 2)
	assert.Equal(t, "user", turns[0][0])
	assert.Equal(t, "hello", turns[0][1])
	assert.Equal(t, "assistant", turns[1][0])
	assert.Equal(t, "[noop]", turns[1][1])

	// Verify flow_state was written.
	var step, payload string
	row := db.QueryRow(`SELECT step, payload FROM flow_state WHERE session_id = ?`, "sess-001")
	require.NoError(t, row.Scan(&step, &payload))
	assert.Equal(t, "", step)
	assert.Equal(t, "", payload)
}

// ------------------------------------------------------------------
// Test: RunLoop end-to-end
// ------------------------------------------------------------------

func TestSession_RunLoop(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	// Register a fake flow that returns "noop" with the input text.
	reg := sessionflow.NewRegistry()
	reg.Register("default", noopFlow{})

	oldReg := sessionflow.DefaultRegistry
	sessionflow.DefaultRegistry = reg
	defer func() { sessionflow.DefaultRegistry = oldReg }()

	orch := NewOrchestrator(OrchestratorConfig{Default: "test", Models: map[string]ModelConfig{}})

	// Simulate two lines of input then EOF.
	in := strings.NewReader("first\nsecond\n")
	var out strings.Builder

	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{
		AgentID:      "agent-x",
		FlowID:       "default",
		TaskID:       "task-x",
		DB:           db,
		Orchestrator: orch,
		In:           in,
		Out:          &out,
	})
	require.NoError(t, err)
	require.NotEmpty(t, s.SessionID)

	// Run until EOF.
	err = s.Run(ctx)
	require.NoError(t, err)

	// Verify turns in DB.
	rows, err := db.Query(`SELECT role, body FROM turns WHERE session_id = ? ORDER BY created_at`, s.SessionID)
	require.NoError(t, err)
	defer rows.Close()

	var turns [][2]string
	for rows.Next() {
		var role, body string
		require.NoError(t, rows.Scan(&role, &body))
		turns = append(turns, [2]string{role, body})
	}
	require.NoError(t, rows.Err())

	require.Len(t, turns, 4) // user+assistant × 2
	assert.Equal(t, "user", turns[0][0])
	assert.Equal(t, "first", turns[0][1])
	assert.Equal(t, "assistant", turns[1][0])
	assert.Equal(t, "[noop]", turns[1][1])
	assert.Equal(t, "user", turns[2][0])
	assert.Equal(t, "second", turns[2][1])
	assert.Equal(t, "assistant", turns[3][0])
	assert.Equal(t, "[noop]", turns[3][1])

	// Verify output contains prompts and markers.
	output := out.String()
	assert.Contains(t, output, "> ")
	assert.Contains(t, output, "Session ended (EOF)")
}

// ------------------------------------------------------------------
// Test: replayTail
// ------------------------------------------------------------------

func TestSession_replayTail(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	sid := "sess-replay"
	_, err := db.Exec(`INSERT INTO sessions (session_id, agent_id, flow_id, status, created_at, updated_at)
	                   VALUES (?, 'agent', 'default', 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, sid)
	require.NoError(t, err)

	// Insert 5 turns out of order to test ordering.
	for i := 1; i <= 5; i++ {
		_, err := db.Exec(
			`INSERT INTO turns (turn_id, session_id, role, body, created_at)
			 VALUES (?, ?, 'user', ?, datetime('now', ?))`,
			uuid.NewString(), sid, "msg-"+string(rune('0'+i)), "+"+string(rune('0'+i))+" seconds")
		require.NoError(t, err)
	}

	s := &Session{SessionID: sid, db: db}
	turns, err := s.replayTail(3)
	require.NoError(t, err)
	require.Len(t, turns, 3)

	// Should be oldest-first of the last 3.
	assert.Equal(t, "msg-3", turns[0].Body)
	assert.Equal(t, "msg-4", turns[1].Body)
	assert.Equal(t, "msg-5", turns[2].Body)
}

// ------------------------------------------------------------------
// Test: resume loads flow state
// ------------------------------------------------------------------

func TestSession_resumeLoadsFlowState(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	// Seed a session with flow state.
	_, err := db.Exec(`INSERT INTO sessions (session_id, agent_id, flow_id, status, created_at, updated_at)
	                   VALUES ('sess-resume', 'agent-r', 'default', 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO flow_state (session_id, step, payload, updated_at)
	                  VALUES ('sess-resume', 'step-42', '{"k":"v"}', CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	reg := sessionflow.NewRegistry()
	reg.Register("default", noopFlow{})

	oldReg := sessionflow.DefaultRegistry
	sessionflow.DefaultRegistry = reg
	defer func() { sessionflow.DefaultRegistry = oldReg }()

	orch := NewOrchestrator(OrchestratorConfig{Default: "test", Models: map[string]ModelConfig{}})

	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{
		AgentID:      "agent-r",
		FlowID:       "default",
		TaskID:       "task-r",
		DB:           db,
		Orchestrator: orch,
		In:           strings.NewReader(""),
		Out:          &strings.Builder{},
	})
	require.NoError(t, err)
	assert.Equal(t, "sess-resume", s.SessionID)
	assert.Equal(t, "step-42", s.step)
	assert.Equal(t, `{"k":"v"}`, string(s.stepState))
}

// ------------------------------------------------------------------
// Test: runTurn with model_complete action
// ------------------------------------------------------------------

func TestSession_runTurn_modelComplete(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	_, err := db.Exec(`INSERT INTO sessions (session_id, agent_id, flow_id, status, created_at, updated_at)
	                   VALUES ('sess-model', 'agent-m', 'default', 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	// Fake flow that returns model_complete.
	reg := sessionflow.NewRegistry()
	reg.Register("default", fakeFlow{action: "model_complete", payload: "echo|hello"})

	oldReg := sessionflow.DefaultRegistry
	sessionflow.DefaultRegistry = reg
	defer func() { sessionflow.DefaultRegistry = oldReg }()

	// Orchestrator with echo tool registered.
	orch := NewOrchestrator(OrchestratorConfig{Default: "test", Models: map[string]ModelConfig{}})

	var out strings.Builder
	s := &Session{
		SessionID: "sess-model",
		AgentID:   "agent-m",
		FlowID:    "default",
		TaskID:    "task-m",
		db:        db,
		flow:      fakeFlow{action: "tool", payload: "echo|hello"},
		orch:      orch,
		in:        strings.NewReader(""),
		out:       &out,
	}

	handoff, err := s.runTurn(context.Background(), "hello")
	require.NoError(t, err)
	require.Nil(t, handoff)

	// Verify assistant turn contains echo output.
	var body string
	row := db.QueryRow(`SELECT body FROM turns WHERE session_id = ? AND role = 'assistant' ORDER BY created_at DESC LIMIT 1`, "sess-model")
	require.NoError(t, row.Scan(&body))
	assert.Equal(t, "hello", body) // echo tool returns args
}

// ------------------------------------------------------------------
// Test: runTurn with done action ends session
// ------------------------------------------------------------------

func TestSession_runTurn_done(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	_, err := db.Exec(`INSERT INTO sessions (session_id, agent_id, flow_id, status, created_at, updated_at)
	                   VALUES ('sess-done', 'agent-d', 'default', 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	reg := sessionflow.NewRegistry()
	reg.Register("default", fakeFlow{action: "done", payload: ""})

	oldReg := sessionflow.DefaultRegistry
	sessionflow.DefaultRegistry = reg
	defer func() { sessionflow.DefaultRegistry = oldReg }()

	orch := NewOrchestrator(OrchestratorConfig{Default: "test", Models: map[string]ModelConfig{}})

	var out strings.Builder
	s := &Session{
		SessionID: "sess-done",
		AgentID:   "agent-d",
		FlowID:    "default",
		TaskID:    "task-d",
		db:        db,
		flow:      fakeFlow{action: "done", payload: ""},
		orch:      orch,
		in:        strings.NewReader(""),
		out:       &out,
	}

	handoff, err := s.runTurn(context.Background(), "bye")
	require.NoError(t, err)
	require.Nil(t, handoff)

	// runTurn returns nil handoff for "done", but the Run loop would exit on next iteration.
	// Verify the done turn was persisted.
	var body string
	row := db.QueryRow(`SELECT body FROM turns WHERE session_id = ? AND role = 'assistant' ORDER BY created_at DESC LIMIT 1`, "sess-done")
	require.NoError(t, row.Scan(&body))
	assert.Equal(t, "[done]", body)
}

// ------------------------------------------------------------------
// Test: runTurn with error action
// ------------------------------------------------------------------

func TestSession_runTurn_errorAction(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	_, err := db.Exec(`INSERT INTO sessions (session_id, agent_id, flow_id, status, created_at, updated_at)
	                   VALUES ('sess-err', 'agent-e', 'default', 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	reg := sessionflow.NewRegistry()
	reg.Register("default", fakeFlow{action: "error", payload: "something broke"})

	oldReg := sessionflow.DefaultRegistry
	sessionflow.DefaultRegistry = reg
	defer func() { sessionflow.DefaultRegistry = oldReg }()

	orch := NewOrchestrator(OrchestratorConfig{Default: "test", Models: map[string]ModelConfig{}})

	var out strings.Builder
	s := &Session{
		SessionID: "sess-err",
		AgentID:   "agent-e",
		FlowID:    "default",
		TaskID:    "task-e",
		db:        db,
		flow:      fakeFlow{action: "error", payload: "something broke"},
		orch:      orch,
		in:        strings.NewReader(""),
		out:       &out,
	}

	handoff, err := s.runTurn(context.Background(), "trigger")
	require.Error(t, err)
	require.Nil(t, handoff)
	assert.Contains(t, err.Error(), "flow error:")

	// Verify error turn persisted.
	var body string
	row := db.QueryRow(`SELECT body FROM turns WHERE session_id = ? AND role = 'assistant' ORDER BY created_at DESC LIMIT 1`, "sess-err")
	require.NoError(t, row.Scan(&body))
	assert.Contains(t, body, "[error]")
}

// ------------------------------------------------------------------
// Test: mid-session handoff swaps flow, updates DB, writes sentinel
// ------------------------------------------------------------------

// handoffFlow is a fake flow that returns a handoff action.
type handoffFlow struct{}

func (handoffFlow) Route(_ context.Context, in sessionflow.RouteInput) (sessionflow.RouteOutput, error) {
	return sessionflow.RouteOutput{
		Action:  "handoff",
		Payload: "default",
	}, nil
}

func TestSession_Handoff(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	// Seed an onboarding session.
	_, err := db.Exec(`INSERT INTO sessions (session_id, agent_id, flow_id, status, created_at, updated_at)
	                   VALUES ('sess-handoff', 'agent-h', 'onboarding', 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	// Register flows.
	reg := sessionflow.NewRegistry()
	reg.Register("onboarding", handoffFlow{})
	reg.Register("default", noopFlow{})

	oldReg := sessionflow.DefaultRegistry
	sessionflow.DefaultRegistry = reg
	defer func() { sessionflow.DefaultRegistry = oldReg }()

	orch := NewOrchestrator(OrchestratorConfig{Default: "test", Models: map[string]ModelConfig{}})

	// Create a temp home dir for the sentinel.
	home := t.TempDir()

	var out strings.Builder
	s := &Session{
		SessionID: "sess-handoff",
		AgentID:   "agent-h",
		FlowID:    "onboarding",
		TaskID:    "task-h",
		Home:      home,
		db:        db,
		flow:      handoffFlow{},
		orch:      orch,
		in:        strings.NewReader(""),
		out:       &out,
	}

	// Execute a turn that triggers the handoff.
	handoff, err := s.runTurn(context.Background(), "go to default")
	require.NoError(t, err)
	require.NotNil(t, handoff)
	assert.Equal(t, "default", handoff.TargetFlow)

	// Simulate what Run loop does: call handleHandoff.
	ctx := context.Background()
	err = s.handleHandoff(ctx, handoff)
	require.NoError(t, err)

	// 1. Verify in-memory flow swap.
	assert.Equal(t, "default", s.FlowID)
	assert.Equal(t, "", s.step)
	assert.Nil(t, s.stepState)

	// 2. Verify DB sessions.flow_id was updated.
	var dbFlowID string
	row := db.QueryRow(`SELECT flow_id FROM sessions WHERE session_id = ?`, "sess-handoff")
	require.NoError(t, row.Scan(&dbFlowID))
	assert.Equal(t, "default", dbFlowID)

	// 3. Verify firstrun sentinel was written because we left onboarding.
	isFirst, err := firstrun.IsFirstRun(home)
	require.NoError(t, err)
	assert.False(t, isFirst, "onboarding sentinel should be written after handoff from onboarding")

	// 4. Verify output contains handoff marker.
	assert.Contains(t, out.String(), "[handoff → default]")
}

// ------------------------------------------------------------------
// Test: Ctrl-C clean cancellation + goroutine leak check
// ------------------------------------------------------------------

// blockingFlow blocks on a channel until the context is cancelled.
type blockingFlow struct {
	blockCh chan struct{}
}

func (f blockingFlow) Route(ctx context.Context, in sessionflow.RouteInput) (sessionflow.RouteOutput, error) {
	select {
	case <-ctx.Done():
		return sessionflow.RouteOutput{}, ctx.Err()
	case <-f.blockCh:
		return sessionflow.RouteOutput{Action: "noop"}, nil
	}
}

func TestSession_CancelTurn(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	_, err := db.Exec(`INSERT INTO sessions (session_id, agent_id, flow_id, status, created_at, updated_at)
	                   VALUES ('sess-cancel', 'agent-c', 'default', 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	reg := sessionflow.NewRegistry()
	blockCh := make(chan struct{})
	reg.Register("default", blockingFlow{blockCh: blockCh})

	oldReg := sessionflow.DefaultRegistry
	sessionflow.DefaultRegistry = reg
	defer func() { sessionflow.DefaultRegistry = oldReg }()

	orch := NewOrchestrator(OrchestratorConfig{Default: "test", Models: map[string]ModelConfig{}})

	// Two lines of input; first will be cancelled, second completes normally.
	in := strings.NewReader("first\nsecond\n")
	var out strings.Builder

	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{
		AgentID:      "agent-c",
		FlowID:       "default",
		TaskID:       "task-c",
		DB:           db,
		Orchestrator: orch,
		In:           in,
		Out:          &out,
	})
	require.NoError(t, err)

	// Cancel the first turn after a short delay.
	go func() {
		time.Sleep(100 * time.Millisecond)
		close(blockCh)
	}()

	// Actually we need to simulate the Run loop but cancel mid-turn.
	// The Run loop sets up per-turn SIGINT handling. We'll simulate by
	// creating a turn context and cancelling it ourselves.
	turnCtx, cancelTurn := context.WithCancel(ctx)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancelTurn()
	}()

	handoff, err := s.runTurn(turnCtx, "first")
	require.Error(t, err)
	require.Nil(t, handoff)
	assert.ErrorIs(t, err, context.Canceled)

	// After cancellation, the user turn should NOT be in the DB (tx rolled back).
	var count int
	row := db.QueryRow(`SELECT COUNT(*) FROM turns WHERE session_id = ?`, s.SessionID)
	require.NoError(t, row.Scan(&count))
	assert.Equal(t, 0, count, "cancelled turn should leave no rows in turns table")

	// Now run a second turn successfully.
	handoff, err = s.runTurn(ctx, "second")
	require.NoError(t, err)

	// Verify only the second turn's user+assistant rows exist.
	rows, err := db.Query(`SELECT role, body FROM turns WHERE session_id = ? ORDER BY created_at`, s.SessionID)
	require.NoError(t, err)
	defer rows.Close()

	var turns [][2]string
	for rows.Next() {
		var role, body string
		require.NoError(t, rows.Scan(&role, &body))
		turns = append(turns, [2]string{role, body})
	}
	require.NoError(t, rows.Err())

	require.Len(t, turns, 2)
	assert.Equal(t, "user", turns[0][0])
	assert.Equal(t, "second", turns[0][1])
	assert.Equal(t, "assistant", turns[1][0])
	assert.Equal(t, "[noop]", turns[1][1])
}

func TestSession_GoroutineLeak(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	reg := sessionflow.NewRegistry()
	reg.Register("default", noopFlow{})

	oldReg := sessionflow.DefaultRegistry
	sessionflow.DefaultRegistry = reg
	defer func() { sessionflow.DefaultRegistry = oldReg }()

	orch := NewOrchestrator(OrchestratorConfig{Default: "test", Models: map[string]ModelConfig{}})

	// Two lines then EOF.
	in := strings.NewReader("hello\nworld\n")
	var out strings.Builder

	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{
		AgentID:      "agent-g",
		FlowID:       "default",
		TaskID:       "task-g",
		DB:           db,
		Orchestrator: orch,
		In:           in,
		Out:          &out,
	})
	require.NoError(t, err)

	// Snapshot goroutine count before.
	before := runtime.NumGoroutine()

	// Run the session to completion (EOF).
	err = s.Run(ctx)
	require.NoError(t, err)

	// Give any straggler goroutines time to exit.
	time.Sleep(200 * time.Millisecond)

	after := runtime.NumGoroutine()
	delta := after - before
	assert.LessOrEqual(t, delta, 2, "goroutine leak detected: delta=%d before=%d after=%d", delta, before, after)
	assert.GreaterOrEqual(t, delta, -2, "unexpected goroutine drop: delta=%d before=%d after=%d", delta, before, after)
}
