// Package identitystore provides a MongoDB-backed store for identity manifest
// documents. Identities are versioned personas that can be activated and
// retired, with exactly one identity per name being active at any time.
package identitystore

import (
	"time"
)

// IdentityStatus enumerates the possible states of an identity version.
type IdentityStatus string

const (
	StatusActive   IdentityStatus = "active"
	StatusInactive IdentityStatus = "inactive"
	StatusRetired IdentityStatus = "retired"
)

// ProviderType enumerates the supported LLM provider types.
type ProviderType string

const (
	ProviderAnthropic       ProviderType = "anthropic"
	ProviderOpenAI          ProviderType = "openai"
	ProviderOpenAICompatible ProviderType = "openai_compatible"
	ProviderOllama         ProviderType = "ollama"
	ProviderGemini         ProviderType = "gemini"
)

// IdentityManifest is the canonical BSON shape for an identity manifest document.
// Each version of an identity (per name) is a separate document with a unique
// version field. Exactly one document per identity name may have status=active.
type IdentityManifest struct {
	// Name is the identity name (e.g., "xander"). Unique per name.
	Name string `bson:"name" json:"name"`
	// Version is the version string (e.g., "v1", "v2"). Unique per name+version.
	Version string `bson:"version" json:"version"`
	// Status is the current status of this version.
	Status IdentityStatus `bson:"status" json:"status"`
	// ProviderProfiles maps provider type to provider-specific system prompt.
	// The text content is portable; the envelope shape differs per provider.
	ProviderProfiles map[ProviderType]string `bson:"provider_profiles" json:"provider_profiles"`
	// Manifest is the canonical text manifest for this identity version.
	Manifest string `bson:"manifest" json:"manifest"`
	// Description is a human-readable description of this identity version.
	Description string `bson:"description" json:"description"`
	// Tags are arbitrary labels for filtering and categorization.
	Tags []string `bson:"tags" json:"tags"`
	// CreatedAt is the creation timestamp.
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	// UpdatedAt is the last update timestamp.
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
	// ActivatedAt is the timestamp when this version became active (if status=active).
	ActivatedAt *time.Time `bson:"activated_at,omitempty" json:"activated_at,omitempty"`
	// RetiredAt is the timestamp when this version was retired (if status=retired).
	RetiredAt *time.Time `bson:"retired_at,omitempty" json:"retired_at,omitempty"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}
