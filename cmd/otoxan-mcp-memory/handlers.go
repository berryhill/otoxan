package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/silas/otoxan/internal/mcp"
	"github.com/silas/otoxan/internal/store/memory"
)

// SaveMemoryArgs is the input schema for save_memory.
type SaveMemoryArgs struct {
	Agent   string   `json:"agent" jsonschema:"required,description=Agent identifier that owns the memory"`
	Content string   `json:"content" jsonschema:"required,description=Text content of the memory"`
	Tags    []string `json:"tags,omitempty" jsonschema:"description=Optional tags for categorization"`
}

// SearchArgs is the input schema for search_memory.
type SearchArgs struct {
	Query string `json:"query" jsonschema:"required,description=Search query text"`
	K     int    `json:"k" jsonschema:"description=Number of results to return (default 5)"`
}

// ListArgs is the input schema for list_memories.
type ListArgs struct {
	Agent string `json:"agent,omitempty" jsonschema:"description=Filter by agent ID"`
	Limit int    `json:"limit,omitempty" jsonschema:"description=Maximum number of results (default 20)"`
}

func registerTools(srv *mcp.Server, store *memory.MemoryStore) {
	srv.Register(mcp.Tool{
		Name:        "save_memory",
		Description: "Save a new memory document for an agent.",
		InputSchema: mcp.SchemaOf[SaveMemoryArgs](),
		Handler:     handleSaveMemory(store),
	})

	srv.Register(mcp.Tool{
		Name:        "search_memory",
		Description: "Search memories using a query string. Returns matching memory IDs and scores.",
		InputSchema: mcp.SchemaOf[SearchArgs](),
		Handler:     handleSearchMemory(store),
	})

	srv.Register(mcp.Tool{
		Name:        "list_memories",
		Description: "List memory documents, optionally filtered by agent.",
		InputSchema: mcp.SchemaOf[ListArgs](),
		Handler:     handleListMemories(store),
	})
}

func handleSaveMemory(store *memory.MemoryStore) func(ctx context.Context, raw json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args SaveMemoryArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.Agent == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "agent is required"}
		}
		if args.Content == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "content is required"}
		}

		mem := &memory.Memory{
			MemoryID:  fmt.Sprintf("mem_%d", time.Now().UnixNano()),
			AgentID:   args.Agent,
			Content:   args.Content,
			Tags:      args.Tags,
			Type:      memory.TypeObservation,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}

		_, err := store.Create(ctx, mem)
		if err != nil {
			return nil, fmt.Errorf("create memory: %w", err)
		}
		return map[string]string{"memory_id": mem.MemoryID}, nil
	}
}

func handleSearchMemory(store *memory.MemoryStore) func(ctx context.Context, raw json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args SearchArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.Query == "" {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: "query is required"}
		}
		if args.K <= 0 {
			args.K = 5
		}

		// MemoryStore.Search expects a vector; when Qdrant is nil we fall back to text search via List.
		if store == nil {
			return nil, fmt.Errorf("store not available")
		}

		// Since we don't have an embedding model in this binary, we do a simple
		// text-prefix match via List and return results. If Qdrant is configured
		// (non-nil client), Search would use vectors, but with nil qdrant it
		// returns nil. We do a basic substring search over content as fallback.
		mems, err := store.List(ctx, memory.ListOptions{Limit: 1000})
		if err != nil {
			return nil, fmt.Errorf("list memories: %w", err)
		}

		var hits []map[string]any
		for _, m := range mems {
			if containsSubstring(m.Content, args.Query) || containsAnyTag(m.Tags, args.Query) {
				hits = append(hits, map[string]any{
					"memory_id":  m.MemoryID,
					"agent_id":   m.AgentID,
					"content":    m.Content,
					"tags":       m.Tags,
					"created_at": m.CreatedAt,
				})
				if len(hits) >= args.K {
					break
				}
			}
		}
		return hits, nil
	}
}

func handleListMemories(store *memory.MemoryStore) func(ctx context.Context, raw json.RawMessage) (any, error) {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var args ListArgs
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, &mcp.RPCError{Code: mcp.CodeInvalidParams, Message: err.Error()}
		}
		if args.Limit <= 0 {
			args.Limit = 20
		}

		mems, err := store.List(ctx, memory.ListOptions{
			AgentID: args.Agent,
			Limit:   args.Limit,
		})
		if err != nil {
			return nil, fmt.Errorf("list memories: %w", err)
		}

		var out []map[string]any
		for _, m := range mems {
			out = append(out, map[string]any{
				"memory_id":  m.MemoryID,
				"agent_id":   m.AgentID,
				"content":    m.Content,
				"tags":       m.Tags,
				"type":       string(m.Type),
				"created_at": m.CreatedAt,
				"updated_at": m.UpdatedAt,
			})
		}
		return out, nil
	}
}

func containsSubstring(s, sub string) bool {
	return len(sub) > 0 && len(s) > 0 && stringSliceContains(s, sub)
}

func stringSliceContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func containsAnyTag(tags []string, query string) bool {
	for _, t := range tags {
		if stringSliceContains(t, query) {
			return true
		}
	}
	return false
}
