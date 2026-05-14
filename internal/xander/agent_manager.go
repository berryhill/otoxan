//go:build xander

package xander

import (
	"context"
	"fmt"
	"time"

	"github.com/silas/otoxan/internal/secrets"
	"github.com/silas/otoxan/pkg/stores/agentregistry"
	"github.com/silas/otoxan/pkg/stores/scopestore"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ------------------------------------------------------------------
// scopeStoreInterface — minimal surface for AgentManager / AgentCreator
// ------------------------------------------------------------------

// scopeStoreInterface is the subset of scopestore.Store that AgentManager
// and AgentCreator need.  Using an interface keeps the manager testable
// without requiring the full store in unit tests.
type scopeStoreInterface interface {
	Grant(ctx context.Context, agentName string, secretPaths []string) (*mongo.UpdateResult, error)
	Get(ctx context.Context, agentName string) (*scopestore.ScopeDoc, error)
	Revoke(ctx context.Context, agentName string) (*mongo.UpdateResult, error)
}

// ------------------------------------------------------------------
// Errors
// ------------------------------------------------------------------

// ErrAgentDisabled is returned when a bundle request targets an agent
// whose status is not ACTIVE.
var ErrAgentDisabled = fmt.Errorf("xander: agent is disabled")

// ------------------------------------------------------------------
// AgentManager — disable, upgrade, grant, revoke
// ------------------------------------------------------------------

// AgentManager orchestrates agent lifecycle operations.
type AgentManager struct {
	registry *agentregistry.Store
	scopes   scopeStoreInterface
	auditor  secrets.Auditor
}

// NewAgentManager builds an AgentManager from the underlying stores.
func NewAgentManager(registry *agentregistry.Store, scopes scopeStoreInterface, audit secrets.Auditor) *AgentManager {
	return &AgentManager{
		registry: registry,
		scopes:   scopes,
		auditor:  audit,
	}
}

// ------------------------------------------------------------------
// DisableAgent
// ------------------------------------------------------------------

// DisableAgent sets an agent's status to INACTIVE and revokes its secret
// scope. The agent can no longer request bundles until re-enabled.
func (am *AgentManager) DisableAgent(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("disable-agent: agent name is required")
	}

	// Verify the agent exists.
	_, err := am.registry.Get(ctx, name)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			am.audit(ctx, name, "disable", false, "agent not found")
			return fmt.Errorf("disable-agent: agent %q not found", name)
		}
		am.audit(ctx, name, "disable", false, fmt.Sprintf("registry lookup: %v", err))
		return fmt.Errorf("disable-agent: registry lookup: %w", err)
	}

	// Set status to INACTIVE.
	_, err = am.registry.Update(ctx, name, bson.M{"status": agentregistry.AgentStatusInactive})
	if err != nil {
		am.audit(ctx, name, "disable", false, fmt.Sprintf("status update: %v", err))
		return fmt.Errorf("disable-agent: status update: %w", err)
	}

	// Revoke secret scope.
	_, err = am.scopes.Revoke(ctx, name)
	if err != nil {
		am.audit(ctx, name, "disable", false, fmt.Sprintf("scope revoke: %v", err))
		return fmt.Errorf("disable-agent: scope revoke: %w", err)
	}

	am.audit(ctx, name, "disable", true, "")
	return nil
}

// ------------------------------------------------------------------
// UpgradeAgent
// ------------------------------------------------------------------

// UpgradeAgentResult carries the outcome of a successful upgrade.
type UpgradeAgentResult struct {
	AgentName string
	OldRole   string
	NewRole   string
	Status    agentregistry.AgentStatus
}

// UpgradeAgent changes an agent's role and ensures its status is ACTIVE.
// If the agent was INACTIVE, it is re-activated (but its scope is NOT
// automatically re-granted — that must be done separately via GrantScope).
func (am *AgentManager) UpgradeAgent(ctx context.Context, name, newRole string) (*UpgradeAgentResult, error) {
	if name == "" {
		return nil, fmt.Errorf("upgrade-agent: agent name is required")
	}
	if newRole == "" {
		return nil, fmt.Errorf("upgrade-agent: new role is required")
	}

	agent, err := am.registry.Get(ctx, name)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			am.audit(ctx, name, "upgrade", false, "agent not found")
			return nil, fmt.Errorf("upgrade-agent: agent %q not found", name)
		}
		am.audit(ctx, name, "upgrade", false, fmt.Sprintf("registry lookup: %v", err))
		return nil, fmt.Errorf("upgrade-agent: registry lookup: %w", err)
	}

	oldRole := agent.Role
	updates := bson.M{"role": newRole}
	if agent.Status != agentregistry.AgentStatusActive {
		updates["status"] = agentregistry.AgentStatusActive
	}

	_, err = am.registry.Update(ctx, name, updates)
	if err != nil {
		am.audit(ctx, name, "upgrade", false, fmt.Sprintf("registry update: %v", err))
		return nil, fmt.Errorf("upgrade-agent: registry update: %w", err)
	}

	// Re-fetch to get canonical state.
	agent, err = am.registry.Get(ctx, name)
	if err != nil {
		// Should never happen — we just updated it.
		agent = &agentregistry.AgentRegistryDoc{
			Name:   name,
			Role:   newRole,
			Status: agentregistry.AgentStatusActive,
		}
	}

	am.audit(ctx, name, "upgrade", true, "")
	return &UpgradeAgentResult{
		AgentName: agent.Name,
		OldRole:   oldRole,
		NewRole:   agent.Role,
		Status:    agent.Status,
	}, nil
}

// ------------------------------------------------------------------
// GrantScope
// ------------------------------------------------------------------

// GrantScopeResult carries the outcome of a successful scope grant.
type GrantScopeResult struct {
	AgentName   string
	SecretPaths []string
}

// GrantScope grants secret paths to an agent. Replaces any existing scope.
func (am *AgentManager) GrantScope(ctx context.Context, name string, paths []string) (*GrantScopeResult, error) {
	if name == "" {
		return nil, fmt.Errorf("grant-scope: agent name is required")
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("grant-scope: at least one secret path is required")
	}

	// Verify the agent exists and is active.
	agent, err := am.registry.Get(ctx, name)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			am.audit(ctx, name, "grant-scope", false, "agent not found")
			return nil, fmt.Errorf("grant-scope: agent %q not found", name)
		}
		am.audit(ctx, name, "grant-scope", false, fmt.Sprintf("registry lookup: %v", err))
		return nil, fmt.Errorf("grant-scope: registry lookup: %w", err)
	}
	if agent.Status != agentregistry.AgentStatusActive {
		am.audit(ctx, name, "grant-scope", false, fmt.Sprintf("agent status is %s", agent.Status))
		return nil, fmt.Errorf("grant-scope: agent %q is not active (status=%s)", name, agent.Status)
	}

	_, err = am.scopes.Grant(ctx, name, paths)
	if err != nil {
		am.audit(ctx, name, "grant-scope", false, fmt.Sprintf("scope grant: %v", err))
		return nil, fmt.Errorf("grant-scope: scope grant: %w", err)
	}

	am.audit(ctx, name, "grant-scope", true, "")
	return &GrantScopeResult{
		AgentName:   name,
		SecretPaths: paths,
	}, nil
}

// ------------------------------------------------------------------
// RevokeScope
// ------------------------------------------------------------------

// RevokeScope removes all secret scope from an agent.
func (am *AgentManager) RevokeScope(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("revoke-scope: agent name is required")
	}

	// Verify the agent exists.
	_, err := am.registry.Get(ctx, name)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			am.audit(ctx, name, "revoke-scope", false, "agent not found")
			return fmt.Errorf("revoke-scope: agent %q not found", name)
		}
		am.audit(ctx, name, "revoke-scope", false, fmt.Sprintf("registry lookup: %v", err))
		return fmt.Errorf("revoke-scope: registry lookup: %w", err)
	}

	_, err = am.scopes.Revoke(ctx, name)
	if err != nil {
		am.audit(ctx, name, "revoke-scope", false, fmt.Sprintf("scope revoke: %v", err))
		return fmt.Errorf("revoke-scope: scope revoke: %w", err)
	}

	am.audit(ctx, name, "revoke-scope", true, "")
	return nil
}

// ------------------------------------------------------------------
// Bundle gate: disabled agents are rejected
// ------------------------------------------------------------------

// CheckAgentActive verifies that an agent exists and has status ACTIVE.
// It returns ErrAgentDisabled if the agent is inactive, retired, or not found.
func (am *AgentManager) CheckAgentActive(ctx context.Context, name string) error {
	agent, err := am.registry.Get(ctx, name)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return ErrAgentDisabled
		}
		return fmt.Errorf("check-agent-active: registry lookup: %w", err)
	}
	if agent.Status != agentregistry.AgentStatusActive {
		return ErrAgentDisabled
	}
	return nil
}

// ------------------------------------------------------------------
// Audit helper
// ------------------------------------------------------------------

func (am *AgentManager) audit(ctx context.Context, agentName, action string, success bool, errMsg string) {
	if am.auditor == nil {
		return
	}
	_ = am.auditor.Record(ctx, secrets.AuditEvent{
		AgentName:   agentName,
		RequestedAt: time.Now().UTC(),
		Success:     success,
		Error:       errMsg,
		Paths:       nil,
	})
}
