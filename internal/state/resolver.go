package state

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ------------------------------------------------------------------
// Name validation
// ------------------------------------------------------------------

// agentNameRe matches valid agent names: lowercase letters, digits, and
// hyphens only. No slashes, underscores, uppercase, or empty strings.
var agentNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// ValidateAgentName returns an error if the name is not a valid agent
// identifier. Valid names are non-empty, lowercase, and contain only
// letters, digits, and hyphens.
func ValidateAgentName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("agent name cannot be empty")
	}
	if !agentNameRe.MatchString(name) {
		return fmt.Errorf("invalid agent name %q: must be lowercase letters, digits, and hyphens only", name)
	}
	return nil
}

// ------------------------------------------------------------------
// Database resolver
// ------------------------------------------------------------------

// AgentDB returns a *mongo.Database for the named agent. The database
// name is prefixed with "otoxan_agent_" to ensure isolation and
// avoid collisions with global otoxan state.
//
// Example: AgentDB("xander") → database "otoxan_agent_xander".
func AgentDB(client *mongo.Client, name string) (*mongo.Database, error) {
	if err := ValidateAgentName(name); err != nil {
		return nil, err
	}
	dbName := "otoxan_agent_" + name
	return client.Database(dbName), nil
}

// GlobalDB returns the shared *mongo.Database for cross-agent application
// state (team registry, directives, dispatch lanes, identity manifests, etc.).
func GlobalDB(client *mongo.Client) *mongo.Database {
	return client.Database("otoxan_global")
}
