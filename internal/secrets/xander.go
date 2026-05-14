package secrets

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/silas/otoxan/pkg/stores/scopestore"
)

// ------------------------------------------------------------------
// Clock (testability)
// ------------------------------------------------------------------

// Clock provides the current time.  The real implementation uses time.Now;
// tests swap in a fakeClock to make expiry deterministic.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// ------------------------------------------------------------------
// AuditEvent
// ------------------------------------------------------------------

// AuditEvent records a single secret-bundle request for compliance and
// debugging.  One event is written for every call to RequestBundle,
// regardless of success or failure.
type AuditEvent struct {
	AgentName   string    `bson:"agent_name" json:"agent_name"`
	RequestedAt time.Time `bson:"requested_at" json:"requested_at"`
	Success     bool      `bson:"success" json:"success"`
	Error       string    `bson:"error,omitempty" json:"error,omitempty"`
	Paths       []string  `bson:"paths" json:"paths"`
}

// Auditor persists audit events.  Production uses MongoDB; tests use a
// slice-backed recorder.
type Auditor interface {
	Record(ctx context.Context, ev AuditEvent) error
}

// ------------------------------------------------------------------
// Bundle
// ------------------------------------------------------------------

// Bundle is a short-lived collection of secrets fetched on behalf of an agent.
// It expires so that a compromised process cannot leak credentials indefinitely.
type Bundle struct {
	// Secrets maps secret name → decrypted value.
	Secrets map[string]string `json:"secrets"`

	// ExpiresAt is when the bundle ceases to be valid.
	ExpiresAt time.Time `json:"expires_at"`

	// clock is the time source used by IsExpired.  nil means realClock.
	clock Clock
}

// IsExpired reports whether the bundle has passed its expiry time.
func (b *Bundle) IsExpired() bool {
	c := b.clock
	if c == nil {
		c = realClock{}
	}
	return c.Now().After(b.ExpiresAt)
}

// ------------------------------------------------------------------
// XanderClient
// ------------------------------------------------------------------

// XanderClient mediates secret access for all otoxan agents.
//
// It is the only component that ever holds the long-lived Infisical admin
// credential.  Every other agent receives short-lived Bundles via
// RequestBundle.  Xander checks the agent's scope in MongoDB before talking
// to Infisical, giving otoxan-side authorization on top of Infisical-side
// authentication.
type XanderClient struct {
	infisical *Client
	scopes    *scopestore.Store
	auditor   Auditor

	// WorkspaceID is the default Infisical project/workspace ID.
	WorkspaceID string

	// Environment is the default Infisical environment slug (e.g. "dev").
	Environment string

	// DefaultTTL is the lifetime of a newly minted Bundle.  Zero means 1h.
	DefaultTTL time.Duration

	// clock is the time source used when minting bundles.  nil means realClock.
	clock Clock
}

// NewXanderClient builds a XanderClient from an Infisical Client and a
// scope Store.
func NewXanderClient(infisical *Client, scopes *scopestore.Store) *XanderClient {
	return &XanderClient{
		infisical:   infisical,
		scopes:      scopes,
		Environment: "dev",
		DefaultTTL:  time.Hour,
	}
}

// SetAuditor injects an auditor after construction (used by tests and
// the IPC server wiring).
func (x *XanderClient) SetAuditor(a Auditor) {
	x.auditor = a
}

// RequestBundle fetches secrets for the named agent.
//
//  1. Looks up the agent's granted scope in MongoDB.
//  2. For each secret path in the scope, resolves the secret from Infisical.
//  3. Returns a Bundle containing the decrypted values.
//
// If the agent has no scope, has been revoked (soft-deleted), or any
// requested secret cannot be fetched, an error is returned.
//
// An audit event is recorded for every call.
func (x *XanderClient) RequestBundle(ctx context.Context, agentName string) (*Bundle, error) {
	now := x.now()

	// 1. Authorise — does this agent have a scope?
	scope, err := x.scopes.Get(ctx, agentName)
	if err != nil {
		x.audit(ctx, agentName, false, fmt.Sprintf("scope lookup: %v", err), nil)
		return nil, fmt.Errorf("xander: scope lookup for %q: %w", agentName, err)
	}
	if scope == nil || len(scope.SecretPaths) == 0 {
		x.audit(ctx, agentName, false, "no secret scope", nil)
		return nil, fmt.Errorf("xander: agent %q has no secret scope", agentName)
	}

	// 2. Resolve defaults.
	workspaceID := x.WorkspaceID
	env := x.Environment
	if env == "" {
		env = "dev"
	}

	// 3. Fetch each secret from Infisical.
	//    For now we treat the path as the raw secret name; wildcard expansion
	//    will be added once the Infisical list endpoint is wired in.
	secrets := make(map[string]string, len(scope.SecretPaths))
	for _, path := range scope.SecretPaths {
		name := path
		val, err := x.infisical.Get(ctx, name, workspaceID, env)
		if err != nil {
			x.audit(ctx, agentName, false, fmt.Sprintf("fetch %q: %v", name, err), scope.SecretPaths)
			return nil, fmt.Errorf("xander: fetch %q for %q: %w", name, agentName, err)
		}
		secrets[name] = val
	}

	// 4. Mint bundle.
	ttl := x.DefaultTTL
	if ttl == 0 {
		ttl = time.Hour
	}
	bundle := &Bundle{
		Secrets:   secrets,
		ExpiresAt: now.Add(ttl),
		clock:     x.clock,
	}

	x.audit(ctx, agentName, true, "", scope.SecretPaths)
	return bundle, nil
}

// now returns the current time via the injected clock (or real time).
func (x *XanderClient) now() time.Time {
	if x.clock != nil {
		return x.clock.Now()
	}
	return time.Now()
}

// audit writes an audit event via the injected auditor (or silently drops it
// if no auditor is configured).
func (x *XanderClient) audit(ctx context.Context, agentName string, success bool, errMsg string, paths []string) {
	if x.auditor == nil {
		return
	}
	_ = x.auditor.Record(ctx, AuditEvent{
		AgentName:   agentName,
		RequestedAt: x.now(),
		Success:     success,
		Error:       errMsg,
		Paths:       paths,
	})
}

// TestCredential probes Infisical with the given client-id and client-secret.
// It returns nil if Infisical accepts the credential (even if the requested
// secret does not exist). It returns an error only on auth failure or network
// issues — a 404 on a non-existent secret name is treated as a successful auth.
func (x *XanderClient) TestCredential(ctx context.Context, clientID, clientSecret string) error {
	if x.infisical == nil {
		return fmt.Errorf("xander: no infisical client")
	}
	tmp := &Client{
		BaseURL:      x.infisical.BaseURL,
		HTTPClient:   x.infisical.HTTPClient,
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}
	_, err := tmp.Get(ctx, "__test_marker__", x.WorkspaceID, x.Environment)
	if err != nil {
		// After a successful auth, the secrets endpoint returns 404 for missing secrets.
		// Any non-404 error from the secrets endpoint means auth failed or network died.
		if strings.Contains(err.Error(), "404") {
			return nil
		}
		return err
	}
	return nil
}

// UpdateCredential replaces the in-memory Infisical client credentials.
// After this call, all subsequent RequestBundle calls use the new credential.
func (x *XanderClient) UpdateCredential(clientID, clientSecret string) {
	x.infisical.ClientID = clientID
	x.infisical.ClientSecret = clientSecret
	// Invalidate the cached token so the next request fetches a fresh one.
	x.infisical.tokenCache = ""
}
