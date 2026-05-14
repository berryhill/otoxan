//go:build xander

package xander

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/silas/otoxan/internal/secrets"
	"github.com/silas/otoxan/pkg/stores/agentregistry"
)
// ------------------------------------------------------------------
// RotateSelf — credential rotation orchestration
// ------------------------------------------------------------------

// RotationPhase tracks where we are in the rotation dance.
type RotationPhase int

const (
	PhasePrompt RotationPhase = iota
	PhaseVerify
	PhaseSwap
	PhaseRefresh
	PhaseDone
)

// RotationState is the in-memory state machine for a rotate-self operation.
type RotationState struct {
	Phase       RotationPhase `json:"phase"`
	StartedAt   time.Time     `json:"started_at"`
	NewClientID string        `json:"new_client_id,omitempty"`
	Message     string        `json:"message,omitempty"`
}

// rotator orchestrates the DS-5 rotation drill.
type rotator struct {
	client       *secrets.XanderClient
	agentManager *AgentManager
	agentCreator *AgentCreator

	notifiers []func(ctx context.Context, agentName string, payload map[string]any)

	mu     sync.Mutex
	states map[string]*RotationState
}

func newRotator(client *secrets.XanderClient, am *AgentManager, ac *AgentCreator) *rotator {
	return &rotator{
		client:       client,
		agentManager: am,
		agentCreator: ac,
		states:       make(map[string]*RotationState),
	}
}

// RotateSelf executes the full rotation drill.
func (r *rotator) RotateSelf(ctx context.Context) (*RotationState, error) {
	id := fmt.Sprintf("rot-%d", time.Now().UnixNano())
	state := &RotationState{Phase: PhasePrompt, StartedAt: time.Now().UTC()}
	r.mu.Lock()
	r.states[id] = state
	r.mu.Unlock()

	// --- Step 1: Prompt ---
	newID, newSecret, err := r.promptNewCredential(ctx)
	if err != nil {
		state.Message = fmt.Sprintf("prompt failed: %v", err)
		return state, fmt.Errorf("rotate-self: prompt: %w", err)
	}
	state.NewClientID = newID
	state.Phase = PhaseVerify

	// --- Step 2: Verify ---
	if err := r.verifyNewCredential(ctx, newID, newSecret); err != nil {
		state.Message = fmt.Sprintf("verify failed: %v", err)
		return state, fmt.Errorf("rotate-self: verify: %w", err)
	}
	state.Phase = PhaseSwap

	// --- Step 3: Swap ---
	if err := r.swapCredential(newID, newSecret); err != nil {
		state.Message = fmt.Sprintf("swap failed: %v", err)
		return state, fmt.Errorf("rotate-self: swap: %w", err)
	}
	state.Phase = PhaseRefresh

	// --- Step 4: Refresh ---
	refreshed, err := r.refreshActiveBundles(ctx)
	if err != nil {
		state.Message = fmt.Sprintf("refresh failed: %v", err)
		return state, fmt.Errorf("rotate-self: refresh: %w", err)
	}
	state.Phase = PhaseDone
	state.Message = fmt.Sprintf("rotation complete; %d bundles refreshed", refreshed)

	return state, nil
}

func (r *rotator) promptNewCredential(ctx context.Context) (string, string, error) {
	// In a real drill this would prompt the operator or read from an HSM.
	// For the test harness we generate a synthetic credential deterministically.
	ts := time.Now().UnixNano()
	return fmt.Sprintf("xander-admin-%d", ts), fmt.Sprintf("secret-%d", ts), nil
}

func (r *rotator) verifyNewCredential(ctx context.Context, newID, newSecret string) error {
	if r.client == nil {
		return fmt.Errorf("no xander client configured")
	}
	return r.client.TestCredential(ctx, newID, newSecret)
}

func (r *rotator) swapCredential(newID, newSecret string) error {
	if r.client == nil {
		return fmt.Errorf("no xander client configured")
	}
	r.client.UpdateCredential(newID, newSecret)
	return nil
}

func (r *rotator) refreshActiveBundles(ctx context.Context) (int, error) {
	if r.agentManager == nil || r.agentManager.registry == nil {
		return 0, fmt.Errorf("agent manager not configured")
	}
	agents, err := r.agentManager.registry.List(ctx, agentregistry.ListOptions{
		Status: []agentregistry.AgentStatus{agentregistry.AgentStatusActive},
	})
	if err != nil {
		return 0, fmt.Errorf("list active agents: %w", err)
	}
	refreshed := 0
	for _, agent := range agents {
		_, err := r.client.RequestBundle(ctx, agent.Name)
		if err != nil {
			continue
		}
		refreshed++
		r.notify(ctx, agent.Name, map[string]any{"op": "bundle_refresh"})
	}
	return refreshed, nil
}

func (r *rotator) notify(ctx context.Context, agentName string, payload map[string]any) {
	for _, fn := range r.notifiers {
		fn(ctx, agentName, payload)
	}
}
