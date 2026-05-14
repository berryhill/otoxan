// cmd_recall.go — otoxan recall subcommand
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/silas/otoxan/internal/config"
	"github.com/silas/otoxan/internal/embedder"
	"github.com/silas/otoxan/internal/qdrant"
	"github.com/silas/otoxan/pkg/recall"
	"github.com/spf13/cobra"
)

func newRecallCmd() *cobra.Command {
	var (
		agent string
		limit int
	)
	cmd := &cobra.Command{
		Use:   "recall <query>",
		Short: "Semantic search across an agent's entire history",
		Long: `recall embeds the query string, searches the agent's Qdrant
index, and prints ranked results with source_type, source_id, score,
and a content preview.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			query := args[0]

			cfg, err := loadConfig()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// Resolve agent
			agentID := agent
			if agentID == "" {
				agentID = flagAgent
			}
			if agentID == "" {
				agentID = cfg.DefaultAgent
			}
			if agentID == "" {
				return fmt.Errorf("--agent is required (or set default_agent in config)")
			}

			// Build embedder from config
			emb, err := buildEmbedder(cfg)
			if err != nil {
				return fmt.Errorf("build embedder: %w", err)
			}

			// Build qdrant client from env/config
			qdrantURL := os.Getenv("OTOXAN_QDRANT_URL")
			if qdrantURL == "" {
				qdrantURL = "http://localhost:6333"
			}
			qc := qdrant.NewClient(qdrantURL)

			collection := fmt.Sprintf("%s_index", agentID)

			results, err := recall.Recall(ctx, query, emb, qc, collection, limit)
			if err != nil {
				return fmt.Errorf("recall: %w", err)
			}

			printRecallTable(results)
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "agent id (required if no default)")
	cmd.Flags().IntVar(&limit, "limit", 10, "max results")
	return cmd
}

// printRecallTable prints results in a tabular format matching DS-5.
func printRecallTable(results []recall.Result) {
	if len(results) == 0 {
		fmt.Println("No results found.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SCORE\tSOURCE_TYPE\tSOURCE_ID\tCONTENT_PREVIEW")
	for _, r := range results {
		preview := r.Content
		if len(preview) > 80 {
			preview = preview[:77] + "..."
		}
		if preview == "" {
			preview = "-"
		}
		fmt.Fprintf(w, "%.4f\t%s\t%s\t%s\n", r.Score, r.SourceType, r.SourceID, preview)
	}
	w.Flush()
}

// buildEmbedder constructs an embedder from configuration.
func buildEmbedder(cfg *config.Config) (embedder.Embedder, error) {
	// For now, use a simple env-based resolution.  The full config
	// embedding block will be wired once otoxan.toml parsing lands.
	provider := os.Getenv("OTOXAN_EMBEDDING_PROVIDER")
	if provider == "" {
		provider = "openai"
	}
	model := os.Getenv("OTOXAN_EMBEDDING_MODEL")
	dimStr := os.Getenv("OTOXAN_EMBEDDING_DIMENSION")
	dimension := 1536
	if dimStr != "" {
		fmt.Sscanf(dimStr, "%d", &dimension)
	}

	switch strings.ToLower(provider) {
	case "openai":
		if model == "" {
			model = "text-embedding-3-small"
		}
		return embedder.NewOpenAIEmbedder("", model, dimension), nil
	case "ollama":
		if model == "" {
			model = "nomic-embed-text"
		}
		baseURL := os.Getenv("OTOXAN_OLLAMA_URL")
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		return embedder.NewOllamaEmbedder(baseURL, model, dimension), nil
	default:
		return nil, fmt.Errorf("unknown embedding provider: %s", provider)
	}
}
