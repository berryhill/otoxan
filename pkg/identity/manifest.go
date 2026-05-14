// Package identity provides a versioned persona system that injects identity
// into LLM interactions. It supports multiple providers (Anthropic, OpenAI,
// Ollama, Gemini) via per-provider adapters.
package identity

import "time"

// Manifest represents a versioned identity manifest. Each version is immutable
// once published; callers request a specific version or "latest".
type Manifest struct {
	// IdentityID is the stable identifier for this identity (e.g., "xander").
	IdentityID string `json:"identity_id"`

	// Version is the semantic version of this manifest (e.g., "4").
	Version string `json:"version"`

	// Name is the display name (e.g., "Xander").
	Name string `json:"name"`

	// Description is a human-readable summary of who this identity is.
	Description string `json:"description"`

	// SystemPrompt is the core persona text injected into the system message.
	SystemPrompt string `json:"system_prompt"`

	// CreatedAt is when this version was published.
	CreatedAt time.Time `json:"created_at"`

	// ProviderOverrides allows per-provider tuning of the system prompt.
	// Keys are provider names: "anthropic", "openai", "ollama", "gemini".
	// If a provider is not listed, the base SystemPrompt is used.
	ProviderOverrides map[string]string `json:"provider_overrides,omitempty"`

	// CacheControl, if true, includes cache breakpoints for Anthropic-style
	// prompt caching. Only applies to the Anthropic adapter.
	CacheControl bool `json:"cache_control,omitempty"`
}
