package identity

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func makeTestManifest() *Manifest {
	return &Manifest{
		IdentityID:   "xander",
		Version:      "4",
		Name:        "Xander",
		Description: "A helpful AI assistant",
		SystemPrompt: "You are Xander, a helpful AI assistant. You are direct, knowledgeable, and efficient.",
		CreatedAt:    time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		CacheControl: false,
		ProviderOverrides: map[string]string{},
	}
}

// -----------------------------------------------------------------------
// Anthropic adapter tests - Table-driven
// -----------------------------------------------------------------------

// TestAnthropicFormat is the table-driven test suite validating Anthropic adapter
// with/without breakpoints and tool_descriptions. Output validates against Anthropic
// Messages API schema.
func TestAnthropicFormat(t *testing.T) {
	adapter := NewAnthropicAdapter()

	// Test data: identity with multi-section persona
	multiSectionManifest := &Manifest{
		IdentityID:   "xander",
		Version:      "4",
		Name:        "Xander",
		Description: "Primary agent persona",
		SystemPrompt: `# IDENTITY
You are Xander, a helpful AI assistant.

## CAPABILITIES
- Code review
- Debugging help
- Architecture advice

## GUIDELINES
- Be thorough
- Show working`,
		CreatedAt:    time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		CacheControl: false,
	}

	// Tool descriptions for testing
	tools := []ToolDescription{
		{
			Name:        "get_weather",
			Description: "Get the current weather for a location",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"location": map[string]any{
						"type":        "string",
						"description": "The city name",
					},
				},
				"required": []any{"location"},
			},
		},
		{
			Name:        "search_code",
			Description: "Search codebase for patterns",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type": "string",
					},
				},
				"required": []any{"query"},
			},
		},
	}

	tests := []struct {
		name        string
		manifest    *Manifest
		breakpoints []CacheBreakpoint
		description string
		validate    func(t *testing.T, result string, block any, tools []ToolDescription)
	}{
		{
			name:     "basic system prompt without breakpoints",
			manifest: makeTestManifest(),
			description: "Basic system prompt format with no cache control",
			validate: func(t *testing.T, result string, block any, tools []ToolDescription) {
				// Result should be plain text
				if result == "" {
					t.Error("expected non-empty result")
				}
				if !strings.Contains(result, "Xander") {
					t.Error("expected system prompt to contain 'Xander'")
				}
				// Should not contain cache_control marker
				if strings.Contains(result, "cache_control") {
					t.Error("should not contain cache_control without breakpoints")
				}
			},
		},
		{
			name:        "system prompt with CacheControl flag",
			manifest:    multiSectionManifest,
			breakpoints: []CacheBreakpoint{CacheBreakEphemeral1H},
			description: "System prompt with cache_control enabled via manifest flag",
			validate: func(t *testing.T, result string, block any, tools []ToolDescription) {
				// Should contain cache_break marker (from FormatSystemWithBreakpoints)
				if !strings.Contains(result, "cache_break:") {
					t.Error("expected cache_break marker in result")
				}
				// Should contain original prompt
				if !strings.Contains(result, "Xander") {
					t.Error("expected system prompt to contain identity text")
				}
			},
		},
		{
			name:        "system prompt with explicit breakpoints",
			manifest:    multiSectionManifest,
			breakpoints: []CacheBreakpoint{CacheBreakEphemeral1H, CacheBreakEphemeral5M, CacheBreakBlock},
			description: "System prompt with explicit cache breakpoints at section boundaries",
			validate: func(t *testing.T, result string, block any, tools []ToolDescription) {
				// Should contain breakpoint markers
				if !strings.Contains(result, "cache_break:") {
					t.Error("expected cache_break markers")
				}
				// Should contain all breakpoint types
				if !strings.Contains(result, string(CacheBreakEphemeral1H)) {
					t.Error("expected ephemeral_1h breakpoint")
				}
				if !strings.Contains(result, string(CacheBreakEphemeral5M)) {
					t.Error("expected ephemeral_5m breakpoint")
				}
				if !strings.Contains(result, string(CacheBreakBlock)) {
					t.Error("expected block breakpoint")
				}
				// Original content should be preserved
				if !strings.Contains(result, "CAPABILITIES") {
					t.Error("expected CAPABILITIES section preserved")
				}
			},
		},
		{
			name:        "tool descriptions formatted for Anthropic",
			manifest:    makeTestManifest(),
			breakpoints: nil,
			description: "Tool definitions formatted for Anthropic tool_use parameter",
			validate: func(t *testing.T, result string, block any, tools []ToolDescription) {
				// Format tools for Anthropic
				formattedTools := adapter.FormatToolUseList(tools)
				if len(formattedTools) != len(tools) {
					t.Errorf("expected %d formatted tools, got %d", len(tools), len(formattedTools))
				}

				// Validate first tool structure (Anthropic format)
				tool := formattedTools[0]
				if tool["name"] != "get_weather" {
					t.Errorf("expected tool name 'get_weather', got %v", tool["name"])
				}
				if tool["description"] == "" {
					t.Error("expected non-empty description")
				}
				if _, ok := tool["input_schema"]; !ok {
					t.Error("expected input_schema in Anthropic tool format")
				}

				// Validate JSON schema compliance
				schema, ok := tool["input_schema"].(map[string]any)
				if !ok {
					t.Fatal("input_schema should be object")
				}
				if schema["type"] != "object" {
					t.Errorf("expected schema type 'object', got %v", schema["type"])
				}
				props, ok := schema["properties"].(map[string]any)
				if !ok {
					t.Fatal("expected properties in schema")
				}
				if _, ok := props["location"]; !ok {
					t.Error("expected 'location' in properties")
				}
			},
		},
		{
			name:        "tool with minimal parameters",
			manifest:    makeTestManifest(),
			breakpoints: nil,
			description: "Tool with only name, no description or parameters",
			validate: func(t *testing.T, result string, block any, tools []ToolDescription) {
				minimalTool := ToolDescription{Name: "simple_tool"}
				formatted := adapter.FormatToolUse(minimalTool)
				if formatted["name"] != "simple_tool" {
					t.Errorf("expected name 'simple_tool', got %v", formatted["name"])
				}
				// Should still have input_schema
				if _, ok := formatted["input_schema"]; !ok {
					t.Error("expected input_schema even for minimal tool")
				}
			},
		},
		{
			name:        "FormatSystemBlock returns correct type",
			manifest:    makeTestManifest(),
			breakpoints: nil,
			description: "FormatSystemBlock returns string when no cache control",
			validate: func(t *testing.T, result string, block any, tools []ToolDescription) {
				// Without CacheControl, should return string
				manifest := makeTestManifest()
				manifest.CacheControl = false
				block = adapter.FormatSystemBlock(manifest)
				if _, ok := block.(string); !ok {
					t.Error("expected string when CacheControl is false")
				}
				if block != manifest.SystemPrompt {
					t.Error("block should equal system prompt when no cache")
				}

				// With CacheControl, should return array
				manifest.CacheControl = true
				block = adapter.FormatSystemBlock(manifest)
				blocks, ok := block.([]map[string]any)
				if !ok {
					t.Fatal("expected []map[string]any when CacheControl is true")
				}
				if len(blocks) != 1 {
					t.Errorf("expected 1 block, got %d", len(blocks))
				}
				if blocks[0]["type"] != "text" {
					t.Errorf("expected type 'text', got %v", blocks[0]["type"])
				}
				if blocks[0]["text"] == "" {
					t.Error("expected non-empty text")
				}
				cc, ok := blocks[0]["cache_control"].(map[string]any)
				if !ok {
					t.Error("expected cache_control in block")
				}
				if cc["type"] != "ephemeral_1h" {
					t.Errorf("expected cache type 'ephemeral_1h', got %v", cc["type"])
				}
			},
		},
		{
			name:        "MarshalSystemParam produces valid JSON",
			manifest:    makeTestManifest(),
			breakpoints: nil,
			description: "MarshalSystemParam serializes system param to JSON",
			validate: func(t *testing.T, result string, block any, tools []ToolDescription) {
				// Without cache control
				manifest := makeTestManifest()
				manifest.CacheControl = false
				jsonStr, err := adapter.MarshalSystemParam(manifest)
				if err != nil {
					t.Fatalf("MarshalSystemParam failed: %v", err)
				}
				// Should be valid JSON (just a string)
				if !json.Valid([]byte(jsonStr)) {
					t.Error("expected valid JSON output")
				}

				// With cache control
				manifest.CacheControl = true
				jsonStr, err = adapter.MarshalSystemParam(manifest)
				if err != nil {
					t.Fatalf("MarshalSystemParam failed with cache: %v", err)
				}
				// Should be valid JSON array
				var parsed any
				if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
				// Should be array with text block
				if arr, ok := parsed.([]any); ok {
					if len(arr) == 0 {
						t.Error("expected non-empty array")
					}
				}
			},
		},
		{
			name:        "provider override takes precedence",
			manifest:    makeTestManifest(),
			breakpoints: nil,
			description: "Anthropic-specific override is used when present",
			validate: func(t *testing.T, result string, block any, tools []ToolDescription) {
				manifest := makeTestManifest()
				manifest.ProviderOverrides = map[string]string{
					"anthropic": "Anthropic-specific persona text",
				}
				output := adapter.FormatSystem(manifest)
				if output != "Anthropic-specific persona text" {
					t.Errorf("expected provider override, got %q", output)
				}
			},
		},
		{
			name:        "no leak of other providers",
			manifest:    makeTestManifest(),
			breakpoints: nil,
			description: "Anthropic adapter ignores other provider overrides",
			validate: func(t *testing.T, result string, block any, tools []ToolDescription) {
				manifest := makeTestManifest()
				manifest.ProviderOverrides = map[string]string{
					"openai": "OpenAI-specific text",
					"ollama": "Ollama-specific text",
					"gemini": "Gemini-specific text",
				}
				output := adapter.FormatSystem(manifest)
				// Should still use the base system prompt
				if output != manifest.SystemPrompt {
					t.Errorf("Anthropic adapter leaked other provider's text; got %q", output)
				}
				if strings.Contains(output, "OpenAI") || strings.Contains(output, "Ollama") || strings.Contains(output, "Gemini") {
					t.Errorf("Anthropic result should not contain other provider's text")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := tt.manifest
			
			// Get formatted result
			var result string
			if len(tt.breakpoints) > 0 {
				result = adapter.FormatSystemWithBreakpoints(manifest, tt.breakpoints)
			} else {
				result = adapter.FormatSystem(manifest)
			}

			// Get system block for validation
			block := adapter.FormatSystemBlock(manifest)

			// Run validation
			tt.validate(t, result, block, tools)
		})
	}
}

// TestAnthropicCacheBreakpoints tests specific cache breakpoint scenarios.
func TestAnthropicCacheBreakpoints(t *testing.T) {
	adapter := NewAnthropicAdapter()

	tests := []struct {
		name        string
		breakpoints []CacheBreakpoint
		wantTypes   []CacheBreakpoint
	}{
		{
			name:        "ephemeral 1h",
			breakpoints: []CacheBreakpoint{CacheBreakEphemeral1H},
			wantTypes:   []CacheBreakpoint{CacheBreakEphemeral1H},
		},
		{
			name:        "ephemeral 5m",
			breakpoints: []CacheBreakpoint{CacheBreakEphemeral5M},
			wantTypes:   []CacheBreakpoint{CacheBreakEphemeral5M},
		},
		{
			name:        "block level",
			breakpoints: []CacheBreakpoint{CacheBreakBlock},
			wantTypes:   []CacheBreakpoint{CacheBreakBlock},
		},
		{
			name:        "multiple breakpoints",
			breakpoints: []CacheBreakpoint{CacheBreakEphemeral1H, CacheBreakEphemeral5M, CacheBreakBlock},
			wantTypes:   []CacheBreakpoint{CacheBreakEphemeral1H, CacheBreakEphemeral5M, CacheBreakBlock},
		},
		{
			name:        "empty breakpoints",
			breakpoints: []CacheBreakpoint{},
			wantTypes:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := &Manifest{
				IdentityID:   "test",
				Version:      "1",
				Name:         "Test",
				SystemPrompt: "You are a test assistant.\n\n## Section 1\nContent one.\n\n## Section 2\nContent two.",
				CreatedAt: time.Now(),
			}

			result := adapter.FormatSystemWithBreakpoints(manifest, tt.breakpoints)

			if len(tt.wantTypes) == 0 {
				// No breakpoints - result should equal original
				if result != manifest.SystemPrompt {
					t.Errorf("expected original prompt, got different result")
				}
				return
			}

			// Verify all expected types are present
			for _, want := range tt.wantTypes {
				if !strings.Contains(result, string(want)) {
					t.Errorf("expected %s in result, got %q", want, result)
				}
			}
		})
	}
}

// TestAnthropicJSONSchemaCompliance validates that generated payloads
// conform to the Anthropic Messages API schema.
func TestAnthropicJSONSchemaCompliance(t *testing.T) {
	adapter := NewAnthropicAdapter()

	identity := &Manifest{
		IdentityID:   "schema-test",
		Version:      "1",
		Name:         "Schema Test",
		SystemPrompt: "You are a test assistant for schema validation.",
		CreatedAt:    time.Now(),
	}

	// Test 1: system parameter as string (no cache)
	t.Run("system as string", func(t *testing.T) {
		identity.CacheControl = false
		block := adapter.FormatSystemBlock(identity)

		// Must be string when no cache control
		if _, ok := block.(string); !ok {
			t.Error("system should be string when no cache control")
		}

		// Verify can be used in API call
		jsonStr, err := adapter.MarshalSystemParam(identity)
		if err != nil {
			t.Fatalf("MarshalSystemParam() error = %v", err)
		}
		if !json.Valid([]byte(jsonStr)) {
			t.Error("expected valid JSON for string system param")
		}
	})

	// Test 2: system parameter as array of blocks (with cache)
	t.Run("system as array with cache", func(t *testing.T) {
		identity.CacheControl = true
		block := adapter.FormatSystemBlock(identity)

		// Must be array with cache control
		blocks, ok := block.([]map[string]any)
		if !ok {
			t.Fatal("system should be array with cache control")
		}

		for i, b := range blocks {
			if b["type"] != "text" {
				t.Errorf("block[%d] type should be 'text', got %v", i, b["type"])
			}
			if b["text"] == "" {
				t.Error("block text should not be empty")
			}
			if _, ok := b["cache_control"]; !ok {
				t.Errorf("block[%d] should have cache_control", i)
			}
		}
	})

	// Test 3: tools parameter structure
	t.Run("tools structure", func(t *testing.T) {
		tools := []ToolDescription{
			{
				Name:        "test_tool",
				Description: "A test tool",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"arg1": map[string]any{"type": "string"},
					},
				},
			},
		}

		formatted := adapter.FormatToolUseList(tools)
		if len(formatted) != 1 {
			t.Fatalf("expected 1 tool, got %d", len(formatted))
		}

		// Validate Anthropic tool_use structure
		tool := formatted[0]
		if tool["name"] != "test_tool" {
			t.Errorf("name = %v, want 'test_tool'", tool["name"])
		}
		if tool["description"] != "A test tool" {
			t.Errorf("description = %v, want 'A test tool'", tool["description"])
		}
		if tool["input_schema"] == nil {
			t.Error("input_schema should not be nil")
		}
	})

	// Test 4: roundtrip JSON marshal/unmarshal
	t.Run("JSON roundtrip", func(t *testing.T) {
		identity.CacheControl = true
		block := adapter.FormatSystemBlock(identity)

		// Marshal to JSON
		b, err := json.Marshal(block)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}

		// Unmarshal back
		var roundtrip any
		if err := json.Unmarshal(b, &roundtrip); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		// Verify structure preserved
		if roundtrip == nil {
			t.Error("block lost in roundtrip")
		}
	})
}

// TestValidateAPIResponse tests response validation.
func TestValidateAPIResponse(t *testing.T) {
	tests := []struct {
		name    string
		resp    string
		wantErr bool
	}{
		{
			name:    "valid response",
			resp:    `{"id":"msg_01","type":"message","role":"assistant","content":[{"type":"text","text":"Hello!"}]}`,
			wantErr: false,
		},
		{
			name:    "wrong role",
			resp:    `{"id":"msg_01","type":"message","role":"user","content":[{"type":"text","text":"Hello!"}]}`,
			wantErr: true,
		},
		{
			name:    "empty content",
			resp:    `{"id":"msg_01","type":"message","role":"assistant","content":[]}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			resp:    `not json at all`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAPIResponse([]byte(tt.resp))
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAPIResponse() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// -----------------------------------------------------------------------
// OpenAI adapter tests
// -----------------------------------------------------------------------

func TestOpenAIFormat(t *testing.T) {
	adapter := NewOpenAIAdapter()
	manifest := makeTestManifest()

	// Get the formatted system text
	systemText := adapter.FormatSystem(manifest)

	// Create a message entry like an API caller would
	msg := adapter.FormatSystemMessage(manifest)

	// Verify it's a valid messages array entry
	if msg.Role != "system" {
		t.Errorf("expected role 'system', got %q", msg.Role)
	}
	if msg.Content != systemText {
		t.Error("message content should match FormatSystem output")
	}
	if msg.Content == "" {
		t.Error("message content should not be empty")
	}

	// Verify it can be marshaled as JSON
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal OpenAI message: %v", err)
	}

	// Verify no Anthropic-specific fields leak
	anthropicFields := []string{"cache_control", "type", "source"}
	for _, field := range anthropicFields {
		if strings.Contains(string(b), field) {
			t.Errorf("OpenAI message should not contain Anthropic field %q", field)
		}
	}
}

func TestOpenAIAdapter_ProviderOverride(t *testing.T) {
	adapter := NewOpenAIAdapter()
	manifest := makeTestManifest()
	manifest.ProviderOverrides = map[string]string{
		"openai": "OpenAI-specific persona text",
	}

	msg := adapter.FormatSystemMessage(manifest)
	if msg.Content != "OpenAI-specific persona text" {
		t.Errorf("expected provider override, got %q", msg.Content)
	}
}

// -----------------------------------------------------------------------
// Ollama adapter tests
// -----------------------------------------------------------------------

// TestOllamaFormat covers llama3 default + alpaca override behavior.
// llama3 is the default Ollama model; alpaca is a common fine-tune that
// benefits from an explicit instruction template override.
func TestOllamaFormat(t *testing.T) {
	adapter := NewOllamaAdapter()

	t.Run("llama3_default", func(t *testing.T) {
		// Without any provider override, FormatSystem returns the base system prompt.
		// This matches llama3's behavior which accepts plain system messages.
		manifest := makeTestManifest()

		result := adapter.FormatSystem(manifest)
		if result != manifest.SystemPrompt {
			t.Errorf("expected base system prompt, got %q", result)
		}
		if !strings.Contains(result, "Xander") {
			t.Error("expected result to contain identity name 'Xander'")
		}
		// Verify no other provider's override leaked in
		if strings.Contains(result, "OpenAI") || strings.Contains(result, "Anthropic") || strings.Contains(result, "Gemini") {
			t.Error("result should not contain other provider's persona text")
		}
	})

	t.Run("alpaca_override", func(t *testing.T) {
		// Alpaca fine-tunes benefit from an explicit instruction template.
		// When ProviderOverrides["ollama"] is set, it takes precedence.
		manifest := makeTestManifest()
		manifest.ProviderOverrides = map[string]string{
			"ollama": "Below is an instruction that describes a task, paired with an input that provides further context. Write a response that appropriately completes the request.\n\n### Instruction:\nYou are Xander, a helpful AI assistant. You are direct, knowledgeable, and efficient.\n\n### Response:",
		}

		result := adapter.FormatSystem(manifest)
		expected := manifest.ProviderOverrides["ollama"]

		if result != expected {
			t.Errorf("expected alpaca override, got %q", result)
		}
		if !strings.Contains(result, "### Instruction:") {
			t.Error("expected alpaca template markers in override")
		}
	})

	t.Run("no_leak_other_providers", func(t *testing.T) {
		// Ollama adapter should ignore overrides for other providers.
		manifest := makeTestManifest()
		manifest.ProviderOverrides = map[string]string{
			"anthropic": "Anthropic-specific text",
			"openai":    "OpenAI-specific text",
			"gemini":    "Gemini-specific text",
		}

		result := adapter.FormatSystem(manifest)
		// Should fall back to base system prompt
		if result != manifest.SystemPrompt {
			t.Errorf("Ollama should ignore other providers' overrides; got %q", result)
		}
		if strings.Contains(result, "Anthropic") || strings.Contains(result, "OpenAI") || strings.Contains(result, "Gemini") {
			t.Error("result should not contain other provider's text")
		}
	})
}

// -----------------------------------------------------------------------
// Gemini adapter tests
// -----------------------------------------------------------------------

func TestGeminiFormat(t *testing.T) {
	adapter := NewGeminiAdapter()
	manifest := makeTestManifest()

	result := adapter.FormatSystem(manifest)
	if result != manifest.SystemPrompt {
		t.Errorf("expected %q, got %q", manifest.SystemPrompt, result)
	}
}

// TestGeminiFormat_SystemInstructionShape validates the Gemini API shape:
// { "system_instruction": { "parts": [{ "text": "..." }] } }
func TestGeminiFormat_SystemInstructionShape(t *testing.T) {
	adapter := NewGeminiAdapter()
	manifest := &Manifest{
		IdentityID:   "xander",
		Version:      "4",
		Name:         "Xander",
		Description:  "A helpful AI assistant",
		SystemPrompt: "You are Xander, a helpful AI assistant.",
		CreatedAt:    time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		CacheControl: false,
		ProviderOverrides: map[string]string{},
	}

	instr := adapter.FormatSystemInstruction(manifest)
	if instr == nil {
		t.Fatal("FormatSystemInstruction returned nil")
	}

	// Must have a non-empty parts array
	if len(instr.Parts) == 0 {
		t.Fatal("expected non-empty parts array")
	}

	// Parts[0].text must contain the system prompt
	if instr.Parts[0].Text == "" {
		t.Error("parts[0].text should not be empty")
	}
	if instr.Parts[0].Text != "You are Xander, a helpful AI assistant." {
		t.Errorf("parts[0].text = %q, want %q", instr.Parts[0].Text, manifest.SystemPrompt)
	}

	// Roundtrip: must survive JSON marshal/unmarshal (this is what goes over the wire)
	b, err := json.Marshal(instr)
	if err != nil {
		t.Fatalf("json.Marshal(system_instruction) failed: %v", err)
	}
	if !json.Valid(b) {
		t.Fatal("system_instruction must produce valid JSON")
	}

	var roundtrip GeminiSystemInstruction
	if err := json.Unmarshal(b, &roundtrip); err != nil {
		t.Fatalf("json.Unmarshal roundtrip failed: %v", err)
	}
	if roundtrip.Parts[0].Text != manifest.SystemPrompt {
		t.Errorf("roundtrip parts[0].text = %q, want %q", roundtrip.Parts[0].Text, manifest.SystemPrompt)
	}
}

// TestGeminiAdapter_FormatSystem tests the basic FormatSystem method.
func TestGeminiAdapter_FormatSystem(t *testing.T) {
	adapter := NewGeminiAdapter()
	manifest := makeTestManifest()

	result := adapter.FormatSystem(manifest)
	if result != manifest.SystemPrompt {
		t.Errorf("expected %q, got %q", manifest.SystemPrompt, result)
	}
}

// -----------------------------------------------------------------------
// Registry tests
// -----------------------------------------------------------------------

func TestNewAdapter(t *testing.T) {
	tests := []struct {
		provider Provider
		wantErr  bool
	}{
		{ProviderAnthropic, false},
		{ProviderOpenAI, false},
		{ProviderOllama, false},
		{ProviderGemini, false},
		{Provider("unknown"), true},
	}

	for _, tt := range tests {
		adapter, err := NewAdapter(tt.provider)
		if tt.wantErr {
			if err == nil {
				t.Errorf("expected error for provider %s", tt.provider)
			}
		} else {
			if err != nil {
				t.Errorf("unexpected error for provider %s: %v", tt.provider, err)
			}
			if adapter == nil {
				t.Errorf("expected non-nil adapter for provider %s", tt.provider)
			}
		}
	}
}

// -----------------------------------------------------------------------
// Integration-style test: full manifest roundtrip
// -----------------------------------------------------------------------

func TestManifest_JSONRoundtrip(t *testing.T) {
	original := &Manifest{
		IdentityID:   "xander",
		Version:      "4",
		Name:         "Xander",
		Description:  "A helpful AI assistant",
		SystemPrompt: "You are Xander, a helpful AI assistant.",
		CreatedAt:    time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		ProviderOverrides: map[string]string{
			"anthropic": "Anthropic version",
			"openai":    "OpenAI version",
		},
		CacheControl: true,
	}

	b, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal manifest: %v", err)
	}

	var parsed Manifest
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("failed to unmarshal manifest: %v", err)
	}

	if parsed.IdentityID != original.IdentityID {
		t.Error("IdentityID mismatch")
	}
	if parsed.Version != original.Version {
		t.Error("Version mismatch")
	}
	if parsed.SystemPrompt != original.SystemPrompt {
		t.Error("SystemPrompt mismatch")
	}
	if parsed.CacheControl != original.CacheControl {
		t.Error("CacheControl mismatch")
	}
	if parsed.ProviderOverrides["anthropic"] != "Anthropic version" {
		t.Error("ProviderOverrides mismatch")
	}
}
