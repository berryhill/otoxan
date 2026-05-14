package dispatch

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistence_ensureSchema(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	err := ensureSchema(db)
	require.NoError(t, err)

	// Verify tables exist by querying sqlite_master.
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name IN ('sessions','turns','flow_state')`)
	require.NoError(t, err)
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		names = append(names, n)
	}
	require.NoError(t, rows.Err())
	assert.ElementsMatch(t, []string{"sessions", "turns", "flow_state"}, names)
}

func TestPersistence_loadOrCreateSession_create(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	sid, resumed, err := loadOrCreateSession(db, "agent-a", "default")
	require.NoError(t, err)
	assert.NotEmpty(t, sid)
	assert.False(t, resumed)

	// Verify row in DB.
	var agent, flow, status string
	row := db.QueryRow(`SELECT agent_id, flow_id, status FROM sessions WHERE session_id = ?`, sid)
	require.NoError(t, row.Scan(&agent, &flow, &status))
	assert.Equal(t, "agent-a", agent)
	assert.Equal(t, "default", flow)
	assert.Equal(t, "open", status)
}

func TestPersistence_loadOrCreateSession_resume(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	// Seed an open session.
	_, err := db.Exec(`INSERT INTO sessions (session_id, agent_id, flow_id, status, created_at, updated_at)
	                   VALUES ('sess-001', 'agent-b', 'onboarding', 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	sid, resumed, err := loadOrCreateSession(db, "agent-b", "onboarding")
	require.NoError(t, err)
	assert.Equal(t, "sess-001", sid)
	assert.True(t, resumed)
}

func TestPersistence_loadOrCreateSession_ignoresClosed(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	// Seed a closed session.
	_, err := db.Exec(`INSERT INTO sessions (session_id, agent_id, flow_id, status, created_at, updated_at)
	                   VALUES ('sess-closed', 'agent-c', 'default', 'closed', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	sid, resumed, err := loadOrCreateSession(db, "agent-c", "default")
	require.NoError(t, err)
	assert.NotEqual(t, "sess-closed", sid)
	assert.False(t, resumed)
}

func TestPersistence_persistTurnTx(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	_, err := db.Exec(`INSERT INTO sessions (session_id, agent_id, flow_id, status, created_at, updated_at)
	                   VALUES ('sess-turn', 'agent', 'default', 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	err = persistTurnTx(tx, "turn-1", "sess-turn", "user", "hello")
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	var role, body string
	row := db.QueryRow(`SELECT role, body FROM turns WHERE turn_id = ?`, "turn-1")
	require.NoError(t, row.Scan(&role, &body))
	assert.Equal(t, "user", role)
	assert.Equal(t, "hello", body)
}

func TestPersistence_persistFlowStateTx(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	require.NoError(t, ensureSchema(db))

	_, err := db.Exec(`INSERT INTO sessions (session_id, agent_id, flow_id, status, created_at, updated_at)
	                   VALUES ('sess-flow', 'agent', 'default', 'open', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
	require.NoError(t, err)

	tx, err := db.Begin()
	require.NoError(t, err)

	err = persistFlowStateTx(tx, "sess-flow", "step-2", `{"foo":"bar"}`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	var step, payload string
	row := db.QueryRow(`SELECT step, payload FROM flow_state WHERE session_id = ?`, "sess-flow")
	require.NoError(t, row.Scan(&step, &payload))
	assert.Equal(t, "step-2", step)
	assert.Equal(t, `{"foo":"bar"}`, payload)

	// Upsert — same session, new step.
	tx2, err := db.Begin()
	require.NoError(t, err)
	err = persistFlowStateTx(tx2, "sess-flow", "step-3", `{"baz":1}`)
	require.NoError(t, err)
	require.NoError(t, tx2.Commit())

	row = db.QueryRow(`SELECT step, payload FROM flow_state WHERE session_id = ?`, "sess-flow")
	require.NoError(t, row.Scan(&step, &payload))
	assert.Equal(t, "step-3", step)
	assert.Equal(t, `{"baz":1}`, payload)
}

// openTestDB creates a temporary sqlite database for the test and returns it.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	dsn := filepath.Join(dir, "state.db")
	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		_ = os.Remove(dsn)
	})
	return db
}
