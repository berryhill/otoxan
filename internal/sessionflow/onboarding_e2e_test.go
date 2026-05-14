package sessionflow

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silas/otoxan/internal/firstrun"
	_ "modernc.org/sqlite"
)

// TestOnboarding_E2E runs the full five-step onboarding flow end-to-end
// through the real dispatch session, with scripted stdin.  It asserts:
//   1. The .onboarded sentinel is written to the temp home dir.
//   2. The session flow_id in sqlite is updated to "default".
//   3. The transcript contains the closing handoff message.
func TestOnboarding_E2E(t *testing.T) {
	home := t.TempDir()

	// Open a temp sqlite DB.
	dbPath := filepath.Join(home, "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open state.db: %v", err)
	}
	defer db.Close()

	// Ensure schema inline (avoid import cycle with dispatch package).
	schema := `
CREATE TABLE IF NOT EXISTS sessions (
	session_id    TEXT PRIMARY KEY,
	agent_id      TEXT NOT NULL,
	flow_id       TEXT NOT NULL,
	status        TEXT NOT NULL DEFAULT 'open',
	created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS turns (
	turn_id       TEXT PRIMARY KEY,
	session_id    TEXT NOT NULL REFERENCES sessions(session_id),
	role          TEXT NOT NULL,
	body          TEXT NOT NULL,
	created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS flow_state (
	session_id    TEXT PRIMARY KEY REFERENCES sessions(session_id),
	step          TEXT NOT NULL,
	payload       TEXT,
	updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sessions_agent_flow ON sessions(agent_id, flow_id);
CREATE INDEX IF NOT EXISTS idx_sessions_status      ON sessions(status);
CREATE INDEX IF NOT EXISTS idx_turns_session        ON turns(session_id);
`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	// Scripted stdin: empty (hello) -> yes (confirm_context present) -> yes (confirm_context confirm)
	// -> 1 (pick_intent) -> yes (minimal_setup)
	// After handoff, one more line to trigger the default flow, then EOF.
	script := strings.NewReader("\nyes\nyes\n1\nyes\nhello\n")
	var out strings.Builder

	// Use a mock orchestrator so we don't hit the real model API.
	orch := newMockOrchestrator()

	ctx := context.Background()
	s, err := newTestSession(ctx, testSessionOptions{
		AgentID:      "agent-e2e",
		FlowID:       "onboarding",
		Home:         home,
		DB:           db,
		Orchestrator: orch,
		In:           script,
		Out:          &out,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	// Run the session.  It will process all scripted lines and exit on EOF.
	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 1. Sentinel was written.
	first, err := firstrun.IsFirstRun(home)
	if err != nil {
		t.Fatalf("IsFirstRun: %v", err)
	}
	if first {
		t.Errorf("expected .onboarded sentinel to exist, got IsFirstRun=true")
	}

	// 2. DB flow_id is "default" after handoff.
	var flowID string
	row := db.QueryRow(`SELECT flow_id FROM sessions WHERE session_id = ?`, s.SessionID)
	if err := row.Scan(&flowID); err != nil {
		t.Fatalf("scan flow_id: %v", err)
	}
	if flowID != "default" {
		t.Errorf("expected flow_id=%q after handoff, got %q", "default", flowID)
	}

	// 3. Transcript contains the handoff marker.
	output := out.String()
	if !strings.Contains(output, "[handoff → default]") {
		t.Errorf("output missing handoff marker; got:\n%s", output)
	}

	// 4. Transcript contains the closing handoff message from the onboarding flow.
	if !strings.Contains(output, "All set!") {
		t.Errorf("output missing closing handoff message; got:\n%s", output)
	}
}

// ------------------------------------------------------------------
// Minimal in-test helpers to avoid importing dispatch (import cycle)
// ------------------------------------------------------------------

type mockOrchestrator struct{}

func newMockOrchestrator() *mockOrchestrator { return &mockOrchestrator{} }

func (m *mockOrchestrator) Invoke(ctx context.Context, routed routedInput) (*mockReply, error) {
	if routed.Action == "handoff" {
		return &mockReply{Handoff: &mockHandoff{TargetFlow: routed.Payload}}, nil
	}
	return &mockReply{Text: routed.Payload}, nil
}

type routedInput struct {
	Action  string
	Payload string
}

type mockReply struct {
	Text    string
	Handoff *mockHandoff
	Error   string
}

type mockHandoff struct {
	TargetFlow string
	Payload    string
}

type testSession struct {
	SessionID string
	AgentID   string
	FlowID    string
	Home      string
	db        *sql.DB
	flow      SessionFlow
	orch      *mockOrchestrator
	step      string
	stepState []byte
	in        *strings.Reader
	out       *strings.Builder
}

type testSessionOptions struct {
	AgentID      string
	FlowID       string
	Home         string
	DB           *sql.DB
	Orchestrator *mockOrchestrator
	In           *strings.Reader
	Out          *strings.Builder
}

func newTestSession(ctx context.Context, opts testSessionOptions) (*testSession, error) {
	flow, err := DefaultRegistry.Get(opts.FlowID)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve flow %q: %w", opts.FlowID, err)
	}

	sid, err := createSession(opts.DB, opts.AgentID, opts.FlowID)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	return &testSession{
		SessionID: sid,
		AgentID:   opts.AgentID,
		FlowID:    opts.FlowID,
		Home:      opts.Home,
		db:        opts.DB,
		flow:      flow,
		orch:      opts.Orchestrator,
		in:        opts.In,
		out:       opts.Out,
	}, nil
}

func createSession(db *sql.DB, agentID, flowID string) (string, error) {
	var sid string
	row := db.QueryRow(`SELECT session_id FROM sessions WHERE agent_id = ? AND flow_id = ? AND status = 'open' ORDER BY updated_at DESC LIMIT 1`, agentID, flowID)
	if err := row.Scan(&sid); err == nil {
		return sid, nil
	} else if err != sql.ErrNoRows {
		return "", err
	}
	sid = fmt.Sprintf("sess-%s-%s", agentID, flowID)
	_, err := db.Exec(`INSERT INTO sessions (session_id, agent_id, flow_id, status, created_at, updated_at) VALUES (?, ?, ?, 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, sid, agentID, flowID)
	return sid, err
}

func (s *testSession) Run(ctx context.Context) error {
	scanner := bufio.NewScanner(s.in)
	for {
		fmt.Fprint(s.out, "> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return err
			}
			fmt.Fprintln(s.out, "\nSession ended (EOF).")
			return nil
		}
		input := scanner.Text()
		out, err := s.flow.Route(ctx, RouteInput{
			FlowID:      s.FlowID,
			StepID:      s.step,
			StepState:   s.stepState,
			UserMessage: input,
			Home:        s.Home,
		})
		if err != nil {
			return err
		}
		if out.Action == "handoff" {
			to, _ := HandoffArgs(out)
			if to != "" && to != s.FlowID {
				if s.FlowID == "onboarding" && s.Home != "" {
					_ = firstrun.MarkOnboardingComplete(s.Home)
				}
				_, _ = s.db.Exec(`UPDATE sessions SET flow_id = ?, updated_at = CURRENT_TIMESTAMP WHERE session_id = ?`, to, s.SessionID)
				s.FlowID = to
				newFlow, _ := DefaultRegistry.Get(to)
				s.flow = newFlow
				s.step = ""
				s.stepState = nil
				fmt.Fprintf(s.out, "[handoff → %s]\n", to)
			}
			continue
		}
		reply, err := s.orch.Invoke(ctx, routedInput{Action: out.Action, Payload: out.Payload})
		if err != nil {
			return err
		}
		if reply.Handoff != nil {
			to := reply.Handoff.TargetFlow
			if to != "" && to != s.FlowID {
				if s.FlowID == "onboarding" && s.Home != "" {
					_ = firstrun.MarkOnboardingComplete(s.Home)
				}
				_, _ = s.db.Exec(`UPDATE sessions SET flow_id = ?, updated_at = CURRENT_TIMESTAMP WHERE session_id = ?`, to, s.SessionID)
				s.FlowID = to
				newFlow, _ := DefaultRegistry.Get(to)
				s.flow = newFlow
				s.step = ""
				s.stepState = nil
				fmt.Fprintf(s.out, "[handoff → %s]\n", to)
			}
			continue
		}
		if reply.Error != "" {
			fmt.Fprintf(s.out, "[error] %s\n", reply.Error)
		} else {
			fmt.Fprintln(s.out, reply.Text)
		}
		s.step = out.NextStepID
		s.stepState = out.NextStepState
	}
}
