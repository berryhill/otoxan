package dispatch

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const schemaSQL = `
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

// ensureSchema creates the sqlite tables and indexes required by the dispatch
// session persistence layer.  It is safe to call multiple times.
func ensureSchema(db *sql.DB) error {
	_, err := db.Exec(schemaSQL)
	return err
}

// loadOrCreateSession looks up the most-recent open session for the given
// agent+flow.  If none exists it creates a new session with a random UUID.
// Returns the session id, a bool that is true when an existing session was
// resumed, and any error.
func loadOrCreateSession(db *sql.DB, agentID, flowID string) (sid string, resumed bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Look for the most-recent open session.
	var existing string
	row := db.QueryRowContext(ctx,
		`SELECT session_id FROM sessions
		 WHERE agent_id = ? AND flow_id = ? AND status = 'open'
		 ORDER BY updated_at DESC LIMIT 1`,
		agentID, flowID)
	if err := row.Scan(&existing); err == nil {
		// Touch updated_at so the session remains the most-recent.
		_, _ = db.ExecContext(ctx,
			`UPDATE sessions SET updated_at = CURRENT_TIMESTAMP WHERE session_id = ?`,
			existing)
		return existing, true, nil
	} else if err != sql.ErrNoRows {
		return "", false, fmt.Errorf("loadOrCreateSession: lookup failed: %w", err)
	}

	// No open session — create one.
	sid = uuid.NewString()
	_, err = db.ExecContext(ctx,
		`INSERT INTO sessions (session_id, agent_id, flow_id, status, created_at, updated_at)
		 VALUES (?, ?, ?, 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		sid, agentID, flowID)
	if err != nil {
		return "", false, fmt.Errorf("loadOrCreateSession: insert failed: %w", err)
	}
	return sid, false, nil
}

// persistTurnTx writes a single turn inside an existing sql.Tx.
func persistTurnTx(tx *sql.Tx, turnID, sessionID, role, body string) error {
	_, err := tx.Exec(
		`INSERT INTO turns (turn_id, session_id, role, body, created_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		turnID, sessionID, role, body)
	if err != nil {
		return fmt.Errorf("persistTurnTx: %w", err)
	}
	return nil
}

// persistFlowStateTx upserts flow step state inside an existing sql.Tx.
func persistFlowStateTx(tx *sql.Tx, sessionID, step, payload string) error {
	_, err := tx.Exec(
		`INSERT INTO flow_state (session_id, step, payload, updated_at)
		 VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(session_id) DO UPDATE SET
			 step = excluded.step,
			 payload = excluded.payload,
			 updated_at = CURRENT_TIMESTAMP`,
		sessionID, step, payload)
	if err != nil {
		return fmt.Errorf("persistFlowStateTx: %w", err)
	}
	return nil
}

// updateSessionFlow updates the flow_id for an existing session row.
func updateSessionFlow(db *sql.DB, sessionID, flowID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := db.ExecContext(ctx,
		`UPDATE sessions SET flow_id = ?, updated_at = CURRENT_TIMESTAMP WHERE session_id = ?`,
		flowID, sessionID)
	if err != nil {
		return fmt.Errorf("updateSessionFlow: %w", err)
	}
	return nil
}
