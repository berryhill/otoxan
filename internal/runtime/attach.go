package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
)

// SessionAttacher is the abstraction that resolves "which session should I
// use?" regardless of entry mode.  Implementations hide the difference between
// opening a local sqlite state.db and calling a remote gateway API.
type SessionAttacher interface {
	// Attach returns an open sqlite *sql.DB (local modes) or a client handle
	// that satisfies the same contract (remote modes).  In v1 only local
	// attachment is fully implemented; remote modes return a stub that panics
	// with a clear "not yet implemented" message.
	//
	// agentID and flowID are used to look up or create the session record.
	// home is the otoxan home directory (usually $HOME/.otoxan).
	Attach(ctx context.Context, agentID, flowID, home string) (*sql.DB, error)
}

// ------------------------------------------------------------------
// localAttacher
// ------------------------------------------------------------------

// localAttacher opens state.db directly on the local filesystem.
type localAttacher struct{}

// Attach opens the sqlite database at home/state.db, ensures the schema,
// and returns the *sql.DB.  The caller (usually dispatch.NewSession or
// dispatch.OpenInteractiveSession) owns the connection lifetime.
func (a *localAttacher) Attach(ctx context.Context, agentID, flowID, home string) (*sql.DB, error) {
	// Open state.db directly.  The dispatch package owns the schema and
	// session lifecycle; we just provide the raw *sql.DB handle.
	db, err := openLocalStateDB(home)
	if err != nil {
		return nil, fmt.Errorf("runtime/local: open state.db: %w", err)
	}
	return db, nil
}

// openLocalStateDB opens the sqlite database at home/state.db.
func openLocalStateDB(home string) (*sql.DB, error) {
	dbPath := filepath.Join(home, "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return db, nil
}

// ------------------------------------------------------------------
// sshAttacher
// ------------------------------------------------------------------

// sshAttacher is a parked design stub for the SSH entry mode.
// It will eventually tunnel state.db access over SSH (e.g. via an SSH
// port-forward to a local sqlite proxy on the workstation).
type sshAttacher struct{}

func (a *sshAttacher) Attach(ctx context.Context, agentID, flowID, home string) (*sql.DB, error) {
	panic("runtime/ssh: session attachment over SSH is not yet implemented (mode parked in design)")
}

// ------------------------------------------------------------------
// gatewayAttacher
// ------------------------------------------------------------------

// gatewayAttacher is a parked design stub for the gateway entry mode.
// It will eventually speak HTTPS/gRPC to the otoxan-gateway service to
// create or resume a remote session.
//
// TODO(otoxan-entry-modes-local-ssh-gateway): Implement real gateway
// attachment once the gateway binary (cmd/otoxan-gateway/) exists and the
// wire protocol is defined.  See plan otoxan-entry-modes-local-ssh-gateway.
type gatewayAttacher struct{}

func (a *gatewayAttacher) Attach(ctx context.Context, agentID, flowID, home string) (*sql.DB, error) {
	panic("runtime/gateway: session attachment via gateway is not yet implemented (mode parked in design)")
}

// ------------------------------------------------------------------
// Registry
// ------------------------------------------------------------------

// attacherRegistry maps each Mode to its SessionAttacher implementation.
// The zero value is ready to use; it is populated on first call to
// AttacherFor.
type attacherRegistry struct {
	local   *localAttacher
	ssh     *sshAttacher
	gateway *gatewayAttacher
}

var defaultAttachers = &attacherRegistry{
	local:   &localAttacher{},
	ssh:     &sshAttacher{},
	gateway: &gatewayAttacher{},
}

// AttacherFor returns the SessionAttacher appropriate for the given mode.
// It always returns a non-nil value; calling Attach on SSH or Gateway
// modes will panic with a clear message until those modes are implemented.
func AttacherFor(m string) SessionAttacher {
	switch m {
	case string(ModeLocal):
		return defaultAttachers.local
	case string(ModeSSH):
		return defaultAttachers.ssh
	case string(ModeGateway):
		return defaultAttachers.gateway
	default:
		// Fallback to local for unknown modes — safe default in v1.
		return defaultAttachers.local
	}
}
