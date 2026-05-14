// Package xander provides the IPC client types used by the otoxan CLI to talk
// to the Xander daemon.  It lives in a tag-free file so that the main otoxan
// binary (built without the xander tag) can still import the wire types and
// SocketPath helper.
package xander

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// ------------------------------------------------------------------
// Wire types — JSON request/response envelope (mirrored from ipc_server.go)
// ------------------------------------------------------------------

// OpType is the operation requested by the client.
type OpType string

const (
	OpRequestBundle OpType = "request_bundle"
	OpHealth        OpType = "health"
	OpAuditTail     OpType = "audit_tail"
	OpCreateAgent   OpType = "create_agent"
	OpListAgents    OpType = "list_agents"
	OpDisableAgent  OpType = "disable_agent"
	OpUpgradeAgent  OpType = "upgrade_agent"
	OpGrantScope    OpType = "grant_scope"
	OpRotateSelf    OpType = "rotate_self"
)

// Request is the JSON envelope sent over the Unix socket by clients.
type Request struct {
	Op      OpType          `json:"op"`
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Response is the JSON envelope returned by the server.
type Response struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// ------------------------------------------------------------------
// Payload shapes (mirrored from ipc_server.go)
// ------------------------------------------------------------------

// CreateAgentPayload carries the name and role for a new agent.
type CreateAgentPayload struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

// ListAgentsPayload configures the list query.
type ListAgentsPayload struct {
	Status         []string `json:"status,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	IncludeDeleted bool     `json:"include_deleted,omitempty"`
}

// DisableAgentPayload carries the agent name to disable.
type DisableAgentPayload struct {
	AgentName string `json:"agent_name"`
}

// UpgradeAgentPayload carries the agent name and new role.
type UpgradeAgentPayload struct {
	AgentName string `json:"agent_name"`
	NewRole   string `json:"new_role"`
}

// GrantScopePayload carries the agent name and secret paths.
type GrantScopePayload struct {
	AgentName   string   `json:"agent_name"`
	SecretPaths []string `json:"secret_paths"`
}

// RequestBundlePayload carries the agent name for a bundle request.
type RequestBundlePayload struct {
	AgentName string `json:"agent_name"`
}

// HealthResult is returned for OpHealth.
type HealthResult struct {
	Status    string `json:"status"`
	Version   string `json:"version"`
	UptimeSec int64  `json:"uptime_sec"`
}

// AuditTailPayload configures the audit tail query.
type AuditTailPayload struct {
	AgentName string `json:"agent_name,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// AuditTailResult carries the matched audit events.
type AuditTailResult struct {
	Events []any `json:"events"` // will be deserialized as []secrets.AuditEvent by the caller
}

// RotateSelfResult is returned for OpRotateSelf.
type RotateSelfResult struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// ------------------------------------------------------------------
// Socket helpers (mirrored from server.go)
// ------------------------------------------------------------------

// SocketPath returns the default Unix socket path for the Xander IPC server.
func SocketPath() string {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, ".otoxan", "run", "xander.sock")
}

// EnsureRunDir creates the ~/.otoxan/run directory if it does not exist.
func EnsureRunDir() error {
	path := filepath.Dir(SocketPath())
	return os.MkdirAll(path, 0700)
}

// ------------------------------------------------------------------
// PeerCredAuthorizer (mirrored from server.go)
// ------------------------------------------------------------------

// PeerCredAuthorizer accepts or rejects Unix-socket connections based on the
// connecting process's UID.  Only connections from the same UID as the server
// are allowed; cross-UID connections are rejected with an error.
type PeerCredAuthorizer struct {
	ExpectedUID uint32
}

// NewPeerCredAuthorizer creates an authorizer that accepts only connections
// from the same UID as the calling process.
func NewPeerCredAuthorizer() (*PeerCredAuthorizer, error) {
	uid := uint32(os.Getuid())
	if uid == 0 {
		return nil, fmt.Errorf("xander: refusing to run as root")
	}
	return &PeerCredAuthorizer{ExpectedUID: uid}, nil
}

// Authorize extracts the peer credentials from a UnixConn and returns an error
// if the remote UID does not match ExpectedUID.
func (a *PeerCredAuthorizer) Authorize(conn *net.UnixConn) error {
	// Stub: the real implementation requires golang.org/x/sys/unix which is
	// only compiled with the xander build tag.  This stub always succeeds so
	// that tag-free consumers can compile.  The daemon (built with -tags=xander)
	// uses the real implementation in server.go.
	return nil
}
