package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/silas/otoxan/internal/llm"
)

const version = "0.1.0-dev"

var (
	flagTaskID       = flag.String("task-id", "", "task identifier")
	flagPrompt       = flag.String("prompt", "", "task prompt (or read from stdin)")
	flagProvider     = flag.String("provider", "mock", "LLM provider name (claude, openrouter, mock)")
	flagModel        = flag.String("model", "", "model override")
	flagConfigPath   = flag.String("config", "", "path to config.yaml")
	flagMarkerDir    = flag.String("marker-dir", "/tmp/otoxan_completed", "directory for completion markers")
	flagSessionID    = flag.String("session-id", "", "dispatch session id")
	flagRequestID    = flag.String("request-id", "", "dispatch request id")
	flagAgentID      = flag.String("agent-id", "", "dispatch agent id")
)

type workerConfig struct {
	Provider     string `yaml:"provider"`
	Model        string `yaml:"model"`
	APIKeySecret string `yaml:"api_key_secret_name"`
}

func main() {
	flag.Parse()

	if *flagTaskID == "" {
		fmt.Fprintln(os.Stderr, "--task-id is required")
		os.Exit(2)
	}

	ctx := context.Background()

	// Resolve prompt.
	prompt := *flagPrompt
	if prompt == "" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read stdin: %v\n", err)
			os.Exit(2)
		}
		prompt = string(b)
	}

	// Load config if provided.
	var cfg workerConfig
	if *flagConfigPath != "" {
		c, err := loadConfig(*flagConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "load config: %v\n", err)
			os.Exit(2)
		}
		cfg = *c
	}

	// Provider / model precedence: flags > config > defaults.
	providerName := firstNonEmpty(*flagProvider, cfg.Provider, "mock")
	modelName := firstNonEmpty(*flagModel, cfg.Model, "")

	provider, err := llm.New(providerName, modelName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "provider init: %v\n", err)
		os.Exit(2)
	}

	// Ensure marker directory exists.
	if err := os.MkdirAll(*flagMarkerDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "marker dir: %v\n", err)
		os.Exit(2)
	}

	markerPath := filepath.Join(*flagMarkerDir, fmt.Sprintf("%s.json", *flagTaskID))

	// Defer writes completion marker even on panic (but NOT on SIGKILL).
	var result *llm.SessionResult
	var runErr error
	var start = time.Now()
	defer func() {
		writeCompletionMarker(markerPath, *flagTaskID, *flagSessionID, result, runErr, time.Since(start))
	}()

	result, runErr = provider.RunSession(ctx, prompt)
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "session error: %v\n", runErr)
		os.Exit(1)
	}

	fmt.Println(result.Output)
	os.Exit(0)
}

func writeCompletionMarker(path, taskID, sessionID string, result *llm.SessionResult, runErr error, elapsed time.Duration) {
	marker := map[string]any{
		"task_id":         taskID,
		"session_id":      sessionID,
		"completed_at":    time.Now().UTC().Format(time.RFC3339),
		"runtime_seconds": int(elapsed.Seconds()),
	}

	if runErr != nil {
		marker["task_status"] = "FAILED"
		marker["exit_code"] = 1
		marker["error_summary"] = runErr.Error()
		marker["output"] = ""
	} else if result != nil {
		marker["task_status"] = "COMPLETED"
		marker["exit_code"] = 0
		marker["output"] = result.Output
		marker["error_summary"] = ""
		if result.TokensUsed > 0 {
			marker["tokens_used"] = result.TokensUsed
		}
		if result.TokensInput > 0 {
			marker["tokens_input"] = result.TokensInput
		}
		if result.TokensOutput > 0 {
			marker["tokens_output"] = result.TokensOutput
		}
	} else {
		marker["task_status"] = "FAILED"
		marker["exit_code"] = 1
		marker["error_summary"] = "no result produced"
		marker["output"] = ""
	}

	b, _ := json.MarshalIndent(marker, "", "  ")
	_ = os.WriteFile(path, b, 0o644)
}

func loadConfig(path string) (*workerConfig, error) {
	// Stub: real implementation would parse YAML.
	// For now, return empty so flags/defaults take over.
	return &workerConfig{}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
