package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/silas/otoxan/internal/mcp"
	"github.com/silas/otoxan/pkg/identity"
	"github.com/silas/otoxan/pkg/stores/identitystore"
)

// ------------------------------------------------------------------
// Arg structs
// ------------------------------------------------------------------

type resolveIdentityArgs struct {
	// Name is the identity name to resolve (e.g., "xander").
	Name string `json:"name" jsonschema:"required,description=Name of the identity to resolve"`
	// Provider is the target LLM provider: anthropic, openai, ollama, gemini.
	// Defaults to "openai" if not specified.
	Provider string `json:"provider,omitempty" jsonschema:"description=Target LLM provider (anthropic, openai, ollama, gemini)"`
	// Version optionally requests a specific version. If empty, resolves active.
	Version string `json:"version,omitempty" jsonschema:"description=Resolve a specific version instead of the active one"`
}

// ------------------------------------------------------------------
// Tool registration
// ------------------------------------------------------------------

func registerTools(srv *mcp.Server, resolver *identity.Resolver) {
	srv.Register(mcp.Tool{
		Name:        "resolve_identity",
		Description: "Resolve an identity manifest and format it for a target LLM provider. Returns the system prompt payload ready for injection into API calls.",
		InputSchema: mcp.SchemaOf[resolveIdentityArgs](),
		Handler:     handleResolveIdentity(resolver),
	})
}

// ------------------------------------------------------------------
// Handlers
// ------------------------------------------------------------------

func handleResolveIdentity(resolver *identity.Resolver) func(context.Context, json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args resolveIdentityArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "invalid arguments: " + err.Error()}
		}
		if args.Name == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "name is required"}
		}

		// Resolve the identity.
		var manifest *identity.Manifest
		var err error

		if args.Version != "" {
			manifest, err = resolver.ResolveVersion(ctx, args.Name, args.Version)
		} else {
			manifest, err = resolver.ResolveActive(ctx, args.Name)
		}

		if err != nil {
			if errors.Is(err, identity.ErrIdentityNotFound) ||
				errors.Is(err, identitystore.ErrNoActiveIdentity) {
				return map[string]any{
					"ok":        false,
					"error":     "no_active_identity",
					"agent":     args.Name,
					"message":   fmt.Sprintf("no active identity found for %q", args.Name),
					"has_prompt": false,
				}, nil
			}
			return nil, &mcp.RPCError{Code: mcp.CodeInternalError, Message: "resolve failed: " + err.Error()}
		}

		// Format for the requested provider.
		provider := identity.Provider(strings.ToLower(args.Provider))
		if provider == "" {
			provider = identity.ProviderOpenAI
		}

		adapter, err := identity.NewAdapter(provider)
		if err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "unknown provider: " + args.Provider}
		}

		systemPrompt := adapter.FormatSystem(manifest)

		// Build provider-specific envelope.
		var envelope any
		switch provider {
		case identity.ProviderAnthropic:
			envelope = adapter.(*identity.AnthropicAdapter).FormatSystemBlock(manifest)
		case identity.ProviderOpenAI:
			msg := adapter.(*identity.OpenAIAdapter).FormatSystemMessage(manifest)
			envelope = map[string]any{
				"role":    msg.Role,
				"content": msg.Content,
			}
		case identity.ProviderGemini:
			inst := adapter.(*identity.GeminiAdapter).FormatSystemInstruction(manifest)
			envelope = map[string]any{
				"parts": inst.Parts,
			}
		case identity.ProviderOllama:
			envelope = map[string]any{
				"role":    "system",
				"content": systemPrompt,
			}
		}

		return map[string]any{
			"ok":          true,
			"agent":       args.Name,
			"version":     manifest.Version,
			"has_prompt":  true,
			"system_prompt": systemPrompt,
			"provider":    string(provider),
			"envelope":    envelope,
			"metadata": map[string]any{
				"identity_id":  manifest.IdentityID,
				"name":        manifest.Name,
				"description": manifest.Description,
				"created_at":  manifest.CreatedAt,
			},
		}, nil
	}
}
