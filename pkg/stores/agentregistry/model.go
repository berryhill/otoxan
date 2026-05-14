// Package agentregistry provides a MongoDB-backed store for the global agent
// registry.
package agentregistry

import "time"

// AgentStatus enumerates the possible states of a registered agent.
type AgentStatus string

const (
	AgentStatusActive   AgentStatus = "ACTIVE"
	AgentStatusInactive AgentStatus = "INACTIVE"
	AgentStatusRetired  AgentStatus = "RETIRED"
)

// AgentRegistryDoc is the canonical BSON shape for an agent document in the
// otoxan_global.agents collection.
type AgentRegistryDoc struct {
	Name      string      `bson:"name" json:"name"`
	Role      string      `bson:"role" json:"role"`
	DBName    string      `bson:"db_name" json:"db_name"`
	Status    AgentStatus `bson:"status" json:"status"`
	CreatedAt time.Time   `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time   `bson:"updated_at" json:"updated_at"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}
