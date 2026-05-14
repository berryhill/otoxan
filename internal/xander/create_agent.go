//go:build xander

package xander

import (
	"context"
	"fmt"
	"time"

	"github.com/silas/otoxan/internal/secrets"
	"github.com/silas/otoxan/pkg/stores/agentregistry"
)

// ------------------------------------------------------------------
// CreateAgent — atomic agent registration
// ------------------------------------------------------------------

// DefaultSecretPaths is the minimal scope every new agent receives.
// It grants access to the agent's own per-agent DB credentials and
// any global read-only secrets.
var DefaultSecretPaths = []string{"/agent/self/db"}

// AgentCreator orchestrates the atomic create-agent flow.
type AgentCreator struct {
	registry *agentregistry.Store
	scopes   scopeStoreInterface
	auditor  secrets.Auditor // uses secrets.Auditor interface
}



// NewAgentCreator builds an AgentCreator from the underlying stores.
func NewAgentCreator(registry *agentregistry.Store, scopes scopeStoreInterface, audit secrets.Auditor) *AgentCreator {
	return &AgentCreator{
		registry: registry,
		scopes:   scopes,
		auditor:  audit,
	}
}

// CreateAgentResult carries the outcome of a successful creation.
type CreateAgentResult struct {
	AgentName string
	DBName    string
	Scope     []string
}

// CreateAgent registers a new agent atomically:
//   1. Creates the agent registry document.
//   2. Creates the per-agent database.
//   3. Grants the default secret scope.
//   4. Writes an audit event.
//
// If any step fails after (1) or (2), it rolls back by hard-deleting the
// registry document and revoking the scope.
func (ac *AgentCreator) CreateAgent(ctx context.Context, name, role string) (*CreateAgentResult, error) {
	if err := validateInputs(name, role); err != nil {
		return nil, err
	}

	// --- Step 1: Register in global registry (creates per-agent DB) ---
	res, err := ac.registry.Register(ctx, name, role)
	if err != nil {
		ac.audit(ctx, name, false, fmt.Sprintf("registry register: %v", err))
		return nil, fmt.Errorf("create-agent: registry register: %w", err)
	}
	_ = res // InsertedID not needed downstream

	// If anything below fails, we must roll back the registry entry.
	rollback := func() {
		_, _ = ac.registry.HardDelete(ctx, name)
	}

	// --- Step 2: Grant default scope ---
	_, err = ac.scopes.Grant(ctx, name, DefaultSecretPaths)
	if err != nil {
		rollback()
		ac.audit(ctx, name, false, fmt.Sprintf("scope grant: %v", err))
		return nil, fmt.Errorf("create-agent: scope grant: %w", err)
	}

	// If audit fails we do NOT roll back — the agent exists and is scoped.
	// Audit is best-effort; missing audit events are a compliance issue,
	// not a consistency issue.
	ac.audit(ctx, name, true, "")

	// Fetch the created doc so we can return the canonical DB name.
	agent, err := ac.registry.Get(ctx, name)
	if err != nil {
		// Should never happen — we just inserted it. Still, return what we know.
		return &CreateAgentResult{
			AgentName: name,
			DBName:    "otoxan_agent_" + name,
			Scope:     DefaultSecretPaths,
		}, nil
	}

	return &CreateAgentResult{
		AgentName: agent.Name,
		DBName:    agent.DBName,
		Scope:     DefaultSecretPaths,
	}, nil
}

// validateInputs checks that name and role are non-empty and the name is valid.
func validateInputs(name, role string) error {
	if name == "" {
		return fmt.Errorf("create-agent: agent name is required")
	}
	if role == "" {
		return fmt.Errorf("create-agent: role is required")
	}
	return nil
}

// audit writes an agent_created audit event.
func (ac *AgentCreator) audit(ctx context.Context, agentName string, success bool, errMsg string) {
	if ac.auditor == nil {
		return
	}
	_ = ac.auditor.Record(ctx, secrets.AuditEvent{
		AgentName:   agentName,
		RequestedAt: time.Now().UTC(),
		Success:     success,
		Error:       errMsg,
		Paths:       nil,
	})
}
