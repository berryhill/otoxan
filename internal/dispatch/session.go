package dispatch

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/google/uuid"
	"github.com/silas/otoxan/internal/firstrun"
	"github.com/silas/otoxan/internal/sessionflow"
)

// ------------------------------------------------------------------
// Session
// ------------------------------------------------------------------

// OpenInteractiveSession opens an interactive dispatch session.
// It chooses the flow based on first-run status, creates/resumes the
// sqlite-backed session, and starts the REPL loop.
func OpenInteractiveSession(ctx context.Context, opts SessionOptions) error {
	if opts.DB == nil {
		return fmt.Errorf("dispatch: DB is required")
	}
	s, err := NewSession(ctx, opts)
	if err != nil {
		return err
	}
	return s.Run(ctx)
}

// Session is an interactive dispatch session backed by sqlite.
// It owns the read → route → invoke → print → commit loop.
type Session struct {
	SessionID string
	AgentID   string
	FlowID    string
	TaskID    string
	Home      string // otoxan home dir; used for firstrun sentinel

	db    *sql.DB
	flow  sessionflow.SessionFlow
	orch  *Orchestrator

	// step is the current flow step id; persisted in flow_state.
	step      string
	stepState []byte

	// in / out are the reader/writer for the REPL.  Defaults to stdin/stdout.
	in  io.Reader
	out io.Writer
}

// SessionOptions carries everything needed to create or resume a session.
type SessionOptions struct {
	AgentID      string
	FlowID       string
	TaskID       string
	Home         string    // otoxan home dir; used for firstrun sentinel
	DB           *sql.DB   // sqlite connection to state.db
	Orchestrator *Orchestrator
	In           io.Reader // nil → os.Stdin
	Out          io.Writer // nil → os.Stdout
}

// NewSession creates a new Session, loading or creating the sqlite session
// record and resolving the flow from the registry.
func NewSession(ctx context.Context, opts SessionOptions) (*Session, error) {
	if opts.DB == nil {
		return nil, fmt.Errorf("dispatch: DB is required")
	}
	if err := ensureSchema(opts.DB); err != nil {
		return nil, fmt.Errorf("dispatch: ensure schema: %w", err)
	}

	flow, err := sessionflow.DefaultRegistry.Get(opts.FlowID)
	if err != nil {
		return nil, fmt.Errorf("dispatch: cannot resolve flow %q: %w", opts.FlowID, err)
	}

	sid, resumed, err := loadOrCreateSession(opts.DB, opts.AgentID, opts.FlowID)
	if err != nil {
		return nil, fmt.Errorf("dispatch: loadOrCreateSession: %w", err)
	}

	s := &Session{
		SessionID: sid,
		AgentID:   opts.AgentID,
		FlowID:    opts.FlowID,
		TaskID:    opts.TaskID,
		Home:      opts.Home,
		db:        opts.DB,
		flow:      flow,
		orch:      opts.Orchestrator,
		in:        opts.In,
		out:       opts.Out,
	}
	if s.in == nil {
		s.in = os.Stdin
	}
	if s.out == nil {
		s.out = os.Stdout
	}

	// If we resumed, load the persisted step state.
	if resumed {
		if err := s.loadFlowState(ctx); err != nil {
			return nil, fmt.Errorf("dispatch: load flow state: %w", err)
		}
	}

	return s, nil
}

// loadFlowState reads step + payload from flow_state for this session.
func (s *Session) loadFlowState(ctx context.Context) error {
	var step string
	var payload sql.NullString
	row := s.db.QueryRowContext(ctx,
		`SELECT step, payload FROM flow_state WHERE session_id = ?`, s.SessionID)
	if err := row.Scan(&step, &payload); err != nil {
		if err == sql.ErrNoRows {
			return nil // no prior state
		}
		return err
	}
	s.step = step
	if payload.Valid {
		s.stepState = []byte(payload.String)
	}
	return nil
}

// ------------------------------------------------------------------
// Run loop
// ------------------------------------------------------------------

// Run starts the interactive REPL.  It blocks until the session ends
// (flow returns "done" or "error") or the outer context is cancelled.
//
// SIGINT during a turn cancels that turn only; the loop continues.
//
// When a turn produces a handoff, the flow is swapped mid-session without
// restart and the loop continues with the new flow attached.
func (s *Session) Run(ctx context.Context) error {
	// Print welcome / resume banner.
	fmt.Fprintln(s.out, "otoxan dispatch session")
	fmt.Fprintf(s.out, "  session: %s  flow: %s  agent: %s\n", s.SessionID, s.FlowID, s.AgentID)
	fmt.Fprintln(s.out, "  type a message and press Enter.  Ctrl-C cancels the current turn.  Ctrl-D exits.")
	fmt.Fprintln(s.out)

	scanner := bufio.NewScanner(s.in)

	for {
		// Prompt.
		fmt.Fprint(s.out, "> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("dispatch: read input: %w", err)
			}
			// EOF (Ctrl-D)
			fmt.Fprintln(s.out, "\nSession ended (EOF).")
			return nil
		}

		input := scanner.Text()
		if input == "" {
			continue
		}

		// Per-turn context with SIGINT cancellation.
		turnCtx, cancelTurn := context.WithCancel(ctx)
		go func() {
			c := make(chan os.Signal, 1)
			signal.Notify(c, os.Interrupt)
			select {
			case <-c:
				cancelTurn()
			case <-turnCtx.Done():
			}
			signal.Stop(c)
		}()

		handoff, err := s.runTurn(turnCtx, input)
		cancelTurn()

		if err != nil {
			// If the turn was cancelled by SIGINT, just print a newline and loop.
			if turnCtx.Err() == context.Canceled {
				fmt.Fprintln(s.out, "\n[turn cancelled]")
				continue
			}
			// Terminal error from the flow or persistence.
			fmt.Fprintf(s.out, "\n[session error: %v]\n", err)
			return err
		}

		// Mid-session handoff: swap flow, update DB, reset step state.
		if handoff != nil {
			if err := s.handleHandoff(ctx, handoff); err != nil {
				fmt.Fprintf(s.out, "\n[handoff error: %v]\n", err)
				return err
			}
		}
	}
}

// handleHandoff swaps the session to a new flow and persists the change.
func (s *Session) handleHandoff(ctx context.Context, h *HandoffRequest) error {
	targetFlow := h.TargetFlow
	if targetFlow == "" || targetFlow == s.FlowID {
		return nil // nothing to do
	}

	newFlow, err := sessionflow.DefaultRegistry.Get(targetFlow)
	if err != nil {
		return fmt.Errorf("cannot resolve flow %q: %w", targetFlow, err)
	}

	// If leaving onboarding, write the completion sentinel.
	if s.FlowID == "onboarding" && s.Home != "" {
		if err := firstrun.MarkOnboardingComplete(s.Home); err != nil {
			// Non-fatal: log but continue.
			fmt.Fprintf(s.out, "[warn] could not write onboarding sentinel: %v\n", err)
		}
	}

	// Update DB atomically.
	if err := updateSessionFlow(s.db, s.SessionID, targetFlow); err != nil {
		return fmt.Errorf("update session flow: %w", err)
	}

	// Swap in-memory state.
	s.flow = newFlow
	s.FlowID = targetFlow
	s.step = ""
	s.stepState = nil

	fmt.Fprintf(s.out, "[handoff → %s]\n", targetFlow)
	return nil
}

// runTurn executes one user → assistant turn inside a single sqlite transaction.
// It returns a *HandoffRequest when the turn ends with a handoff action,
// signalling the Run loop to swap flows mid-session.
func (s *Session) runTurn(ctx context.Context, input string) (*HandoffRequest, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	// Rollback is safe after Commit; use defer for safety.
	defer func() { _ = tx.Rollback() }()

	// 1. Persist user turn.
	userTurnID := uuid.NewString()
	if err := persistTurnTx(tx, userTurnID, s.SessionID, "user", input); err != nil {
		return nil, fmt.Errorf("persist user turn: %w", err)
	}

	// 2. Route through the flow.
	out, err := s.flow.Route(ctx, sessionflow.RouteInput{
		FlowID:      s.FlowID,
		StepID:      s.step,
		StepState:   s.stepState,
		UserMessage: input,
		SessionID:   s.SessionID,
		TaskID:      s.TaskID,
		Agent:       s.AgentID,
	})
	if err != nil {
		return nil, fmt.Errorf("flow route: %w", err)
	}

	// 3. Invoke orchestrator (if action demands it).
	var reply *Reply
	if out.Action == "model_complete" || out.Action == "tool" || out.Action == "noop" || out.Action == "handoff" {
		reply, err = s.orch.Invoke(ctx, Routed{Action: out.Action, Payload: out.Payload})
		if err != nil {
			return nil, fmt.Errorf("orchestrator invoke: %w", err)
		}
	} else if out.Action == "done" {
		fmt.Fprintln(s.out, "Session complete.")
		// Persist assistant turn marking completion.
		_ = persistTurnTx(tx, uuid.NewString(), s.SessionID, "assistant", "[done]")
		_ = persistFlowStateTx(tx, s.SessionID, out.NextStepID, string(out.NextStepState))
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}
		return nil, nil // ends Run loop
	} else if out.Action == "error" {
		_ = persistTurnTx(tx, uuid.NewString(), s.SessionID, "assistant", "[error] "+out.Error)
		_ = persistFlowStateTx(tx, s.SessionID, out.NextStepID, string(out.NextStepState))
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit: %w", err)
		}
		return nil, fmt.Errorf("flow error: %s", out.Error)
	} else {
		return nil, fmt.Errorf("unknown flow action: %s", out.Action)
	}

	// 4. Persist assistant turn.
	assistantBody := reply.Text
	if reply.Error != "" {
		assistantBody = "[error] " + reply.Error
	}
	if out.Action == "noop" && assistantBody == "" {
		assistantBody = "[noop]"
	}
	assistantTurnID := uuid.NewString()
	if err := persistTurnTx(tx, assistantTurnID, s.SessionID, "assistant", assistantBody); err != nil {
		return nil, fmt.Errorf("persist assistant turn: %w", err)
	}

	// 5. Persist flow state.
	if err := persistFlowStateTx(tx, s.SessionID, out.NextStepID, string(out.NextStepState)); err != nil {
		return nil, fmt.Errorf("persist flow state: %w", err)
	}

	// 6. Commit the whole turn atomically.
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	// 7. Update in-memory step tracking.
	s.step = out.NextStepID
	s.stepState = out.NextStepState

	// 8. Print reply.
	if reply.Error != "" {
		fmt.Fprintf(s.out, "[error] %s\n", reply.Error)
	} else if reply.Handoff != nil {
		fmt.Fprintf(s.out, "[handoff → %s]\n", reply.Handoff.TargetFlow)
	} else {
		fmt.Fprintln(s.out, reply.Text)
	}

	// 9. Return handoff so Run loop can swap flows mid-session.
	return reply.Handoff, nil
}

// ------------------------------------------------------------------
// replayTail
// ------------------------------------------------------------------

// replayTail returns the last n turns for this session, ordered by created_at.
func (s *Session) replayTail(n int) ([]Turn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx,
		`SELECT turn_id, session_id, role, body, created_at
		 FROM turns
		 WHERE session_id = ?
		 ORDER BY created_at DESC
		 LIMIT ?`,
		s.SessionID, n)
	if err != nil {
		return nil, fmt.Errorf("replayTail: %w", err)
	}
	defer rows.Close()

	var turns []Turn
	for rows.Next() {
		var t Turn
		if err := rows.Scan(&t.TurnID, &t.SessionID, &t.Role, &t.Body, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("replayTail scan: %w", err)
		}
		turns = append(turns, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("replayTail rows: %w", err)
	}

	// Reverse so oldest first.
	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}
	return turns, nil
}

// Turn mirrors a single row from the turns table.
type Turn struct {
	TurnID    string
	SessionID string
	Role      string
	Body      string
	CreatedAt time.Time
}
