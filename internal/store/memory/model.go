// Package memory provides a MongoDB-backed store for agent memory documents
// with soft-delete semantics. Vector storage is delegated to Qdrant.
package memory

import (
	"time"
)

// MemoryType enumerates the kinds of memory entries.
type MemoryType string

const (
	TypeObservation MemoryType = "observation"
	TypeReflection  MemoryType = "reflection"
	TypeFact        MemoryType = "fact"
	TypeExperience  MemoryType = "experience"
)

// Memory is the canonical BSON shape for a memory document.
type Memory struct {
	MemoryID   string     `bson:"memory_id" json:"memory_id"`
	AgentID    string     `bson:"agent_id" json:"agent_id"`
	SessionID  *string    `bson:"session_id,omitempty" json:"session_id,omitempty"`
	Type       MemoryType `bson:"type" json:"type"`
	Content    string     `bson:"content" json:"content"`
	Tags       []string   `bson:"tags" json:"tags"`
	Vector     []float32  `bson:"vector,omitempty" json:"vector,omitempty"`
	VectorID   string     `bson:"vector_id,omitempty" json:"vector_id,omitempty"`
	Importance float64    `bson:"importance" json:"importance"`
	CreatedAt  time.Time  `bson:"created_at" json:"created_at"`
	UpdatedAt  time.Time  `bson:"updated_at" json:"updated_at"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// QdrantPayload returns a map suitable for Qdrant point payload.
func (m *Memory) QdrantPayload() map[string]interface{} {
	return map[string]interface{}{
		"memory_id":  m.MemoryID,
		"agent_id":   m.AgentID,
		"type":       string(m.Type),
		"content":    m.Content,
		"tags":       m.Tags,
		"importance": m.Importance,
		"created_at": m.CreatedAt,
	}
}
