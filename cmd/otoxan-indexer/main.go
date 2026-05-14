// otoxan-indexer is the standalone indexer process that polls MongoDB and
// writes embeddings to Qdrant. It supports both daemon (polling) mode and
// one-shot backfill mode.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/silas/otoxan/internal/config"
	"github.com/silas/otoxan/internal/embedder"
	"github.com/silas/otoxan/internal/index"
	"github.com/silas/otoxan/internal/qdrant"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var (
	flagAgent         = flag.String("agent", "", "agent id (required)")
	flagBackfill      = flag.Bool("backfill", false, "run one-shot backfill of all documents, then exit")
	flagOnce          = flag.Bool("once", false, "alias for --backfill: run one-shot and exit")
	flagProgressEvery = flag.Int("progress-every", 100, "log progress every N docs during backfill")
	flagPollInterval  = flag.Duration("poll-interval", 60*time.Second, "polling interval in daemon mode")
	flagBatchSize     = flag.Int("batch-size", 32, "embedding batch size")
	flagVectorSize    = flag.Int("vector-size", 1536, "embedding vector dimension")
	flagQdrantURL     = flag.String("qdrant-url", "", "Qdrant URL (default: $OTOXAN_QDRANT_URL or http://localhost:6333)")
	flagEmbedding     = flag.String("embedding", "", "embedding provider: openai|ollama (default: $OTOXAN_EMBEDDING_PROVIDER or openai)")
	flagModel         = flag.String("model", "", "embedding model (default: provider-specific default)")
	flagHome          = flag.String("home", "", "otoxan home directory")
)

func main() {
	flag.Parse()

	if *flagAgent == "" {
		fmt.Fprintln(os.Stderr, "--agent is required")
		os.Exit(2)
	}

	ctx := context.Background()

	// Load config for MongoDB credentials.
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Connect to MongoDB.
	mongoURI := cfg.MongoURI
	if mongoURI == "" {
		mongoURI = os.Getenv("OTOXAN_MONGO_URI")
	}
	if mongoURI == "" {
		mongoURI = os.Getenv("MONGO_URI")
	}
	if mongoURI == "" {
		fmt.Fprintln(os.Stderr, "mongo URI not available: set OTOXAN_MONGO_URI or MONGO_URI")
		os.Exit(2)
	}
	mongoDB := cfg.MongoDB
	if mongoDB == "" {
		mongoDB = os.Getenv("OTOXAN_MONGO_DB")
	}
	if mongoDB == "" {
		mongoDB = "otoxan"
	}

	mongoClient, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect mongo: %v\n", err)
		os.Exit(2)
	}
	defer func() { _ = mongoClient.Disconnect(ctx) }()

	// Agent database: <mongo_db>_<agent_id>
	db := mongoClient.Database(mongoDB + "_" + *flagAgent)

	// Build Qdrant client.
	qdrantURL := *flagQdrantURL
	if qdrantURL == "" {
		qdrantURL = os.Getenv("OTOXAN_QDRANT_URL")
	}
	if qdrantURL == "" {
		qdrantURL = "http://localhost:6333"
	}
	qc := qdrant.NewClient(qdrantURL)

	// Build embedder.
	emb, err := buildEmbedder()
	if err != nil {
		log.Fatalf("build embedder: %v", err)
	}

	// Validate vector size matches embedder.
	if emb.Dimension() != *flagVectorSize {
		log.Fatalf("embedder dimension %d does not match --vector-size %d", emb.Dimension(), *flagVectorSize)
	}

	// Build indexer config.
	ixCfg := index.IndexerConfig{
		BatchSize:    *flagBatchSize,
		PollInterval: *flagPollInterval,
		VectorSize:   *flagVectorSize,
	}

	// Build sources — all collections an agent might produce.
	sources := defaultSources()

	// Build pointer store.
	pointerColl := db.Collection("memory_pointers")
	pointerStore := index.NewPointerStore(pointerColl)

	// Build indexer.
	qdrantColl := fmt.Sprintf("%s_index", *flagAgent)
	ix := index.NewIndexer(ixCfg, qc, emb, pointerStore, sources, qdrantColl)

	// Ensure Qdrant collection exists.
	if err := ix.EnsureCollection(ctx); err != nil {
		log.Fatalf("ensure qdrant collection: %v", err)
	}

	if *flagBackfill || *flagOnce {
		mode := "backfill"
		if *flagOnce {
			mode = "once"
		}
		log.Printf("[otoxan-indexer] starting %s for agent %s", mode, *flagAgent)
		if err := ix.Backfill(ctx, *flagProgressEvery); err != nil {
			log.Fatalf("backfill failed: %v", err)
		}
		log.Printf("[otoxan-indexer] %s complete for agent %s", mode, *flagAgent)
		return
	}

	// Daemon mode: poll forever.
	log.Printf("[otoxan-indexer] starting daemon for agent %s (poll=%s)", *flagAgent, *flagPollInterval)
	for {
		if err := ix.RunOnce(ctx); err != nil {
			log.Printf("[otoxan-indexer] cycle error: %v", err)
		}
		select {
		case <-time.After(*flagPollInterval):
		case <-ctx.Done():
			return
		}
	}
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

func loadConfig() (*config.Config, error) {
	home := *flagHome
	if home == "" {
		home = resolveHome()
	}
	return config.Load(home)
}

func resolveHome() string {
	if v := os.Getenv("OTOXAN_HOME"); v != "" {
		return v
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return xdg + "/otoxan"
	}
	home, _ := os.UserHomeDir()
	return home + "/.local/share/otoxan"
}

func buildEmbedder() (index.Embedder, error) {
	provider := *flagEmbedding
	if provider == "" {
		provider = os.Getenv("OTOXAN_EMBEDDING_PROVIDER")
	}
	if provider == "" {
		provider = "openai"
	}

	model := *flagModel
	if model == "" {
		model = os.Getenv("OTOXAN_EMBEDDING_MODEL")
	}

	dimStr := os.Getenv("OTOXAN_EMBEDDING_DIMENSION")
	dimension := *flagVectorSize
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

// defaultSources returns the standard set of MongoDB collections to index.
func defaultSources() []index.SourceConfig {
	return []index.SourceConfig{
		{CollectionName: "plans", SourceType: "plan", Extractor: func(doc bson.M) string { return index.PlanExtractor().Extract(doc) }},
		{CollectionName: "tasks", SourceType: "task", Extractor: func(doc bson.M) string { return index.TaskExtractor().Extract(doc) }},
		{CollectionName: "reports", SourceType: "report", Extractor: func(doc bson.M) string { return index.ReportExtractor().Extract(doc) }},
		{CollectionName: "directives", SourceType: "directive", Extractor: func(doc bson.M) string { return index.DirectiveExtractor().Extract(doc) }},
		{CollectionName: "task_events", SourceType: "task_event", Extractor: func(doc bson.M) string { return index.TaskEventExtractor().Extract(doc) }},
		{CollectionName: "notifications", SourceType: "notification", Extractor: func(doc bson.M) string { return index.NotificationExtractor().Extract(doc) }},
		{CollectionName: "flows", SourceType: "flow", Extractor: func(doc bson.M) string { return index.FlowExtractor().Extract(doc) }},
		{CollectionName: "sessions", SourceType: "session", Extractor: func(doc bson.M) string { return index.SessionExtractor().Extract(doc) }},
	}
}
