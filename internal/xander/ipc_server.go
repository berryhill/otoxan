//go:build xander

package xander

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"sync"

	"github.com/silas/otoxan/internal/secrets"
	"github.com/silas/otoxan/internal/version"
	"github.com/silas/otoxan/pkg/stores/agentregistry"
)

// ------------------------------------------------------------------
// Payload shapes (wire types live in client_types.go, shared with tag-free builds)
// ------------------------------------------------------------------

// IPCCreateAgentResult is the wire shape returned for OpCreateAgent.
// Defined here (not client_types.go) because it references agentregistry types.
type IPCCreateAgentResult struct {
	AgentName string   `json:"agent_name"`
	DBName    string   `json:"db_name"`
	Scope     []string `json:"scope"`
}

// IPCListAgentsResult is the wire shape returned for OpListAgents.
type IPCListAgentsResult struct {
	Agents []agentregistry.AgentRegistryDoc `json:"agents"`
}

// IPCUpgradeAgentResult is the wire shape returned for OpUpgradeAgent.
type IPCUpgradeAgentResult struct {
	AgentName string `json:"agent_name"`
	OldRole   string `json:"old_role"`
	NewRole   string `json:"new_role"`
	Status    string `json:"status"`
}

// IPCGrantScopeResult is the wire shape returned for OpGrantScope.
type IPCGrantScopeResult struct {
	AgentName   string   `json:"agent_name"`
	SecretPaths []string `json:"secret_paths"`
}

// ------------------------------------------------------------------
// Server
// ------------------------------------------------------------------

// Server is the Xander IPC daemon. It listens on a Unix socket, validates
// peer credentials, and dispatches JSON-encoded operations.
type Server struct {
	listener net.Listener
	auth     *PeerCredAuthorizer

	// Dependencies injected at construction time.
	client       *secrets.XanderClient
	agentManager *AgentManager
	agentCreator *AgentCreator

	// In-memory audit ring buffer (captured events for audit_tail).
	auditMu   sync.RWMutex
	auditRing []secrets.AuditEvent
	auditCap  int

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	start  time.Time
	wg     sync.WaitGroup

	// Configured socket path (may differ from default SocketPath()).
	cfgSocketPath string

	// rotator orchestrates credential rotation (lazy-initialised on first use).
	rotator *rotator
}

// ServerConfig holds all dependencies needed to construct a Server.
type ServerConfig struct {
	SocketPath   string
	Client       *secrets.XanderClient
	AgentManager *AgentManager
	AgentCreator *AgentCreator
	AuditCap     int // ring-buffer capacity for in-memory audit tail
}

// NewServer builds a Server from the provided config. It does NOT start
// listening; call Serve() to begin accepting connections.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.SocketPath == "" {
		cfg.SocketPath = SocketPath()
	}
	if cfg.AuditCap == 0 {
		cfg.AuditCap = 1000
	}
	auth, err := NewPeerCredAuthorizer()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		auth:          auth,
		client:        cfg.Client,
		agentManager:  cfg.AgentManager,
		agentCreator:  cfg.AgentCreator,
		auditCap:      cfg.AuditCap,
		cfgSocketPath: cfg.SocketPath,
		ctx:           ctx,
		cancel:        cancel,
		start:         time.Now(),
	}, nil
}

// Serve creates the Unix socket and blocks accepting connections until
// Shutdown is called or the context is cancelled.
func (s *Server) Serve() error {
	path := s.socketPath()
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("xander: remove stale socket: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("xander: ensure run dir: %w", err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("xander: listen %s: %w", path, err)
	}
	// Restrict socket permissions to owner only.
	if err := os.Chmod(path, 0600); err != nil {
		_ = l.Close()
		return fmt.Errorf("xander: chmod socket: %w", err)
	}
	s.listener = l

	for {
		conn, err := l.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return nil
			default:
				return fmt.Errorf("xander: accept: %w", err)
			}
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(conn)
		}()
	}
}

// socketPath returns the configured socket path, falling back to the default.
func (s *Server) socketPath() string {
	if s.cfgSocketPath != "" {
		return s.cfgSocketPath
	}
	return SocketPath()
}

// Shutdown stops the listener and waits for active handlers to finish.
func (s *Server) Shutdown(ctx context.Context) error {
	s.cancel()
	if s.listener != nil {
		_ = s.listener.Close()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// handleConn validates peer credentials, reads requests, and writes responses.
func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	uc, ok := conn.(*net.UnixConn)
	if !ok {
		// Should never happen with unix listener, but guard anyway.
		return
	}
	if err := s.auth.Authorize(uc); err != nil {
		// Log and drop — no response to unauthorized peer.
		return
	}

	br := bufio.NewReader(conn)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				// malformed or truncated
			}
			return
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = s.writeResponse(conn, Response{ID: "", OK: false, Error: "invalid json"})
			continue
		}

		resp := s.dispatch(req)
		if err := s.writeResponse(conn, resp); err != nil {
			return // client gone
		}
	}
}

// dispatch routes the request to the correct handler.
func (s *Server) dispatch(req Request) Response {
	switch req.Op {
	case OpRequestBundle:
		return s.handleRequestBundle(req)
	case OpHealth:
		return s.handleHealth(req)
	case OpAuditTail:
		return s.handleAuditTail(req)
	case OpCreateAgent:
		return s.handleCreateAgent(req)
	case OpListAgents:
		return s.handleListAgents(req)
	case OpDisableAgent:
		return s.handleDisableAgent(req)
	case OpUpgradeAgent:
		return s.handleUpgradeAgent(req)
	case OpGrantScope:
		return s.handleGrantScope(req)
	case OpRotateSelf:
		return s.handleRotateSelf(req)
	default:
		return Response{ID: req.ID, OK: false, Error: fmt.Sprintf("unknown op %q", req.Op)}
	}
}

// ------------------------------------------------------------------
// Op handlers
// ------------------------------------------------------------------

func (s *Server) handleRequestBundle(req Request) Response {
	var p RequestBundlePayload
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		return Response{ID: req.ID, OK: false, Error: "invalid payload"}
	}
	if p.AgentName == "" {
		return Response{ID: req.ID, OK: false, Error: "agent_name required"}
	}

	// Gate: agent must be active.
	if s.agentManager != nil {
		if err := s.agentManager.CheckAgentActive(s.ctx, p.AgentName); err != nil {
			return Response{ID: req.ID, OK: false, Error: err.Error()}
		}
	}

	bundle, err := s.client.RequestBundle(s.ctx, p.AgentName)
	if err != nil {
		return Response{ID: req.ID, OK: false, Error: err.Error()}
	}

	// Capture audit event in ring buffer.
	s.recordAudit(secrets.AuditEvent{
		AgentName:   p.AgentName,
		RequestedAt: time.Now().UTC(),
		Success:     true,
		Paths:       nil, // populated by XanderClient internally via its own auditor
	})

	b, _ := json.Marshal(bundle)
	return Response{ID: req.ID, OK: true, Result: b}
}

func (s *Server) handleHealth(req Request) Response {
	uptime := time.Since(s.start).Seconds()
	res := HealthResult{
		Status:    "ok",
		Version:   version.String(),
		UptimeSec: int64(uptime),
	}
	b, _ := json.Marshal(res)
	return Response{ID: req.ID, OK: true, Result: b}
}

func (s *Server) handleAuditTail(req Request) Response {
	var p AuditTailPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		return Response{ID: req.ID, OK: false, Error: "invalid payload"}
	}
	if p.Limit <= 0 {
		p.Limit = 50
	}

	s.auditMu.RLock()
	events := make([]secrets.AuditEvent, 0, len(s.auditRing))
	for _, ev := range s.auditRing {
		if p.AgentName != "" && ev.AgentName != p.AgentName {
			continue
		}
		events = append(events, ev)
	}
	s.auditMu.RUnlock()

	// Return most recent first, capped at Limit.
	if len(events) > p.Limit {
		events = events[len(events)-p.Limit:]
	}
	// Reverse so newest is first.
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}

	res := struct {
		Events []secrets.AuditEvent `json:"events"`
	}{Events: events}
	b, _ := json.Marshal(res)
	return Response{ID: req.ID, OK: true, Result: b}
}

func (s *Server) handleCreateAgent(req Request) Response {
	var p CreateAgentPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		return Response{ID: req.ID, OK: false, Error: "invalid payload"}
	}
	if p.Name == "" {
		return Response{ID: req.ID, OK: false, Error: "name required"}
	}
	if p.Role == "" {
		p.Role = "worker"
	}
	if s.agentCreator == nil {
		return Response{ID: req.ID, OK: false, Error: "agent creator not configured"}
	}
	res, err := s.agentCreator.CreateAgent(s.ctx, p.Name, p.Role)
	if err != nil {
		return Response{ID: req.ID, OK: false, Error: err.Error()}
	}
	b, _ := json.Marshal(IPCCreateAgentResult{
		AgentName: res.AgentName,
		DBName:    res.DBName,
		Scope:     res.Scope,
	})
	return Response{ID: req.ID, OK: true, Result: b}
}

func (s *Server) handleListAgents(req Request) Response {
	var p ListAgentsPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		return Response{ID: req.ID, OK: false, Error: "invalid payload"}
	}
	if s.agentManager == nil {
		return Response{ID: req.ID, OK: false, Error: "agent manager not configured"}
	}
	statuses := make([]agentregistry.AgentStatus, len(p.Status))
	for i, st := range p.Status {
		statuses[i] = agentregistry.AgentStatus(st)
	}
	agents, err := s.agentManager.registry.List(s.ctx, agentregistry.ListOptions{
		Status:         statuses,
		Limit:          p.Limit,
		IncludeDeleted: p.IncludeDeleted,
	})
	if err != nil {
		return Response{ID: req.ID, OK: false, Error: err.Error()}
	}
	b, _ := json.Marshal(struct {
		Agents []agentregistry.AgentRegistryDoc `json:"agents"`
	}{Agents: agents})
	return Response{ID: req.ID, OK: true, Result: b}
}

func (s *Server) handleDisableAgent(req Request) Response {
	var p DisableAgentPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		return Response{ID: req.ID, OK: false, Error: "invalid payload"}
	}
	if p.AgentName == "" {
		return Response{ID: req.ID, OK: false, Error: "agent_name required"}
	}
	if s.agentManager == nil {
		return Response{ID: req.ID, OK: false, Error: "agent manager not configured"}
	}
	if err := s.agentManager.DisableAgent(s.ctx, p.AgentName); err != nil {
		return Response{ID: req.ID, OK: false, Error: err.Error()}
	}
	return Response{ID: req.ID, OK: true, Result: json.RawMessage(`{"status":"disabled"}`)}
}

func (s *Server) handleUpgradeAgent(req Request) Response {
	var p UpgradeAgentPayload
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		return Response{ID: req.ID, OK: false, Error: "invalid payload"}
	}
	if p.AgentName == "" {
		return Response{ID: req.ID, OK: false, Error: "agent_name required"}
	}
	if p.NewRole == "" {
		return Response{ID: req.ID, OK: false, Error: "new_role required"}
	}
	if s.agentManager == nil {
		return Response{ID: req.ID, OK: false, Error: "agent manager not configured"}
	}
	res, err := s.agentManager.UpgradeAgent(s.ctx, p.AgentName, p.NewRole)
	if err != nil {
		return Response{ID: req.ID, OK: false, Error: err.Error()}
	}
	b, _ := json.Marshal(IPCUpgradeAgentResult{
		AgentName: res.AgentName,
		OldRole:   res.OldRole,
		NewRole:   res.NewRole,
		Status:    string(res.Status),
	})
	return Response{ID: req.ID, OK: true, Result: b}
}

func (s *Server) handleGrantScope(req Request) Response {
	var p GrantScopePayload
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		return Response{ID: req.ID, OK: false, Error: "invalid payload"}
	}
	if p.AgentName == "" {
		return Response{ID: req.ID, OK: false, Error: "agent_name required"}
	}
	if len(p.SecretPaths) == 0 {
		return Response{ID: req.ID, OK: false, Error: "secret_paths required"}
	}
	if s.agentManager == nil {
		return Response{ID: req.ID, OK: false, Error: "agent manager not configured"}
	}
	res, err := s.agentManager.GrantScope(s.ctx, p.AgentName, p.SecretPaths)
	if err != nil {
		return Response{ID: req.ID, OK: false, Error: err.Error()}
	}
	b, _ := json.Marshal(IPCGrantScopeResult{
		AgentName:   res.AgentName,
		SecretPaths: res.SecretPaths,
	})
	return Response{ID: req.ID, OK: true, Result: b}
}

func (s *Server) handleRotateSelf(req Request) Response {
	if s.rotator == nil {
		s.rotator = newRotator(s.client, s.agentManager, s.agentCreator)
		s.rotator.notifiers = append(s.rotator.notifiers, func(ctx context.Context, agentName string, payload map[string]any) {
			// In production this pushes SSE/WebSocket notifications to the agent.
			// For now, log to stdout so operators can observe the drift.
			fmt.Printf("[xander rotation] agent=%s op=%s\n", agentName, payload["op"])
		})
	}
	state, err := s.rotator.RotateSelf(s.ctx)
	if err != nil {
		b, _ := json.Marshal(RotateSelfResult{Status: "error", Message: err.Error()})
		return Response{ID: req.ID, OK: false, Error: err.Error(), Result: b}
	}
	phaseName := "unknown"
	switch state.Phase {
	case PhaseDone:
		phaseName = "complete"
	case PhaseSwap:
		phaseName = "swapping"
	case PhaseRefresh:
		phaseName = "refreshing"
	case PhaseVerify:
		phaseName = "verifying"
	default:
		phaseName = "prompting"
	}
	b, _ := json.Marshal(RotateSelfResult{Status: phaseName, Message: state.Message})
	return Response{ID: req.ID, OK: true, Result: b}
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

func (s *Server) writeResponse(w io.Writer, resp Response) error {
	b, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

func (s *Server) recordAudit(ev secrets.AuditEvent) {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	s.auditRing = append(s.auditRing, ev)
	if len(s.auditRing) > s.auditCap {
		s.auditRing = s.auditRing[len(s.auditRing)-s.auditCap:]
	}
}
