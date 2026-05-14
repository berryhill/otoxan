package identity

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Provider identifies a supported LLM backend.
type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOpenAI    Provider = "openai"
	ProviderOllama   Provider = "ollama"
	ProviderGemini   Provider = "gemini"
)

// CacheBreakpoint controls where Anthropic-style prompt cache breaks occur.
type CacheBreakpoint string

const (
	// CacheBreakEphemeral1H creates a cache breakpoint with a 1-hour TTL.
	CacheBreakEphemeral1H CacheBreakpoint = "ephemeral_1h"
	// CacheBreakEphemeral5M creates a cache breakpoint with a 5-minute TTL.
	CacheBreakEphemeral5M CacheBreakpoint = "ephemeral_5m"
	// CacheBreakBlock creates a block-level cache breakpoint.
	CacheBreakBlock CacheBreakpoint = "block"
)

// ToolDescription represents a tool definition for injection into system prompts.
type ToolDescription struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// Adapter formats an identity manifest for a specific LLM provider.
type Adapter interface {
	// FormatSystem returns the system prompt formatted for the target provider.
	FormatSystem(manifest *Manifest) string
}

// -----------------------------------------------------------------------
// Anthropic adapter
// -----------------------------------------------------------------------

// AnthropicAdapter formats identity for the Anthropic Messages API.
// It supports cache_control breakpoints for prompt caching and tool descriptions.
type AnthropicAdapter struct{}

// NewAnthropicAdapter creates an AnthropicAdapter.
func NewAnthropicAdapter() *AnthropicAdapter {
	return &AnthropicAdapter{}
}

// FormatSystem returns the system prompt, optionally with cache breakpoints.
// When manifest.CacheControl is true, it includes cache_control markers.
func (a *AnthropicAdapter) FormatSystem(manifest *Manifest) string {
	prompt := a.resolvePrompt(manifest)
	if manifest.CacheControl {
		// Anthropic prompt caching: wrap the system in a cache_control block.
		// The breakpoint signals to Anthropic that this prefix can be cached.
		return prompt + "\n\n[Banner: cache_control block_type=any中央]"
	}
	return prompt
}

// FormatSystemWithBreakpoints returns the system prompt with explicit cache breakpoints.
// This method provides fine-grained control over cache insertion points.
func (a *AnthropicAdapter) FormatSystemWithBreakpoints(manifest *Manifest, breakpoints []CacheBreakpoint) string {
	prompt := a.resolvePrompt(manifest)
	if len(breakpoints) == 0 {
		return prompt
	}

	// Find section boundaries and insert breakpoints
	lines := strings.Split(prompt, "\n")
	var result []string
	var currentSection strings.Builder
	sectionIdx := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isSectionHeader := strings.HasPrefix(trimmed, "##") ||
			(len(trimmed) > 3 && trimmed == strings.ToUpper(trimmed) && strings.HasSuffix(trimmed, ":"))

		if isSectionHeader && currentSection.Len() > 0 {
			// End of a section - insert breakpoint if we have one
			if sectionIdx < len(breakpoints) {
				result = append(result, currentSection.String())
				result = append(result, fmt.Sprintf("<!-- cache_break: type=%s -->", breakpoints[sectionIdx]))
				sectionIdx++
			} else {
				result = append(result, currentSection.String())
			}
			currentSection.Reset()
		}
		currentSection.WriteString(line)
		if !strings.HasSuffix(currentSection.String(), "\n") {
			currentSection.WriteString("\n")
		}
	}

	// Don't forget the last section
	if currentSection.Len() > 0 {
		if sectionIdx < len(breakpoints) {
			result = append(result, currentSection.String())
			result = append(result, fmt.Sprintf("<!-- cache_break: type=%s -->", breakpoints[sectionIdx]))
		} else {
			result = append(result, currentSection.String())
		}
	}

	return strings.Join(result, "")
}

// FormatSystemBlock returns the system parameter for the Anthropic Messages API.
// Returns either a string or a structured block with cache_control.
func (a *AnthropicAdapter) FormatSystemBlock(manifest *Manifest) any {
	prompt := a.resolvePrompt(manifest)
	if manifest.CacheControl {
		return []map[string]any{
			{
				"type":          "text",
				"text":          prompt,
				"cache_control": map[string]any{"type": "ephemeral_1h"},
			},
		}
	}
	return prompt
}

// FormatToolUse creates a tool_use definition for the Anthropic API.
func (a *AnthropicAdapter) FormatToolUse(tool ToolDescription) map[string]any {
	inputSchema := tool.Parameters
	if inputSchema == nil {
		inputSchema = map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	return map[string]any{
		"name":         tool.Name,
		"description":  tool.Description,
		"input_schema": inputSchema,
	}
}

// FormatToolUseList creates a tool list for the Anthropic API tools parameter.
func (a *AnthropicAdapter) FormatToolUseList(tools []ToolDescription) []map[string]any {
	result := make([]map[string]any, len(tools))
	for i, tool := range tools {
		result[i] = a.FormatToolUse(tool)
	}
	return result
}

// MarshalSystemParam serializes the system parameter for API calls.
func (a *AnthropicAdapter) MarshalSystemParam(manifest *Manifest) (string, error) {
	block := a.FormatSystemBlock(manifest)
	b, err := json.Marshal(block)
	if err != nil {
		return "", fmt.Errorf("marshal system param: %w", err)
	}
	return string(b), nil
}

// APIResponse represents a parsed Anthropic API response.
type APIResponse struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Role       string `json:"role"`
	Model      string `json:"model"`
	Content    []any  `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// ValidateAPIResponse checks that an API response has expected structure.
func ValidateAPIResponse(resp []byte) error {
	var parsed APIResponse
	if err := json.Unmarshal(resp, &parsed); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if parsed.Role != "assistant" {
		return fmt.Errorf("expected role 'assistant', got %q", parsed.Role)
	}
	if len(parsed.Content) == 0 {
		return fmt.Errorf("empty content in response")
	}
	return nil
}

func (a *AnthropicAdapter) resolvePrompt(manifest *Manifest) string {
	if override, ok := manifest.ProviderOverrides[string(ProviderAnthropic)]; ok && override != "" {
		return override
	}
	return manifest.SystemPrompt
}

// -----------------------------------------------------------------------
// OpenAI adapter
// -----------------------------------------------------------------------

// OpenAIMessage represents a single message in an OpenAI-compatible messages array.
type OpenAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OpenAIAdapter formats identity for OpenAI-compatible APIs.
// Returns a single system message suitable for the messages array.
type OpenAIAdapter struct{}

// NewOpenAIAdapter creates an OpenAIAdapter.
func NewOpenAIAdapter() *OpenAIAdapter {
	return &OpenAIAdapter{}
}

// FormatSystem returns a system message entry suitable for an OpenAI messages array.
func (a *OpenAIAdapter) FormatSystem(manifest *Manifest) string {
	return a.resolvePrompt(manifest)
}

// FormatSystemMessage returns an OpenAIMessage for use in the messages array.
func (a *OpenAIAdapter) FormatSystemMessage(manifest *Manifest) *OpenAIMessage {
	return &OpenAIMessage{
		Role:    "system",
		Content: a.FormatSystem(manifest),
	}
}

func (a *OpenAIAdapter) resolvePrompt(manifest *Manifest) string {
	if override, ok := manifest.ProviderOverrides[string(ProviderOpenAI)]; ok && override != "" {
		return override
	}
	return manifest.SystemPrompt
}

// -----------------------------------------------------------------------
// Ollama adapter
// -----------------------------------------------------------------------

// OllamaAdapter formats identity for Ollama's chat API.
// Ollama uses chat templates per model; we return a plain system message.
type OllamaAdapter struct{}

// NewOllamaAdapter creates an OllamaAdapter.
func NewOllamaAdapter() *OllamaAdapter {
	return &OllamaAdapter{}
}

// FormatSystem returns the system prompt formatted for Ollama.
func (a *OllamaAdapter) FormatSystem(manifest *Manifest) string {
	return a.resolvePrompt(manifest)
}

func (a *OllamaAdapter) resolvePrompt(manifest *Manifest) string {
	if override, ok := manifest.ProviderOverrides[string(ProviderOllama)]; ok && override != "" {
		return override
	}
	return manifest.SystemPrompt
}

// -----------------------------------------------------------------------
// Gemini adapter
// -----------------------------------------------------------------------

// GeminiAdapter formats identity for Google Gemini's API.
// Gemini uses contents/systemInstruction structure.
type GeminiAdapter struct{}

// NewGeminiAdapter creates a GeminiAdapter.
func NewGeminiAdapter() *GeminiAdapter {
	return &GeminiAdapter{}
}

// FormatSystem returns the system prompt text for Gemini.
func (a *GeminiAdapter) FormatSystem(manifest *Manifest) string {
	return a.resolvePrompt(manifest)
}

// GeminiSystemInstruction represents the system_instruction object in Gemini API requests.
// Gemini API expects: { "system_instruction": { "parts": [{ "text": "..." }] } }
type GeminiSystemInstruction struct {
	Parts []GeminiPart `json:"parts"`
}

// GeminiPart represents a part object within a Gemini contents or system_instruction array.
type GeminiPart struct {
	Text string `json:"text,omitempty"`
}

// FormatSystemInstruction returns the system_instruction payload for the Gemini REST API.
// Use this when building a Gemini generateContent request body.
func (a *GeminiAdapter) FormatSystemInstruction(manifest *Manifest) *GeminiSystemInstruction {
	return &GeminiSystemInstruction{
		Parts: []GeminiPart{
			{Text: a.resolvePrompt(manifest)},
		},
	}
}

func (a *GeminiAdapter) resolvePrompt(manifest *Manifest) string {
	if override, ok := manifest.ProviderOverrides[string(ProviderGemini)]; ok && override != "" {
		return override
	}
	return manifest.SystemPrompt
}

// -----------------------------------------------------------------------
// Registry
// -----------------------------------------------------------------------

// NewAdapter returns the appropriate adapter for the given provider.
func NewAdapter(provider Provider) (Adapter, error) {
	switch provider {
	case ProviderAnthropic:
		return NewAnthropicAdapter(), nil
	case ProviderOpenAI:
		return NewOpenAIAdapter(), nil
	case ProviderOllama:
		return NewOllamaAdapter(), nil
	case ProviderGemini:
		return NewGeminiAdapter(), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}
}
