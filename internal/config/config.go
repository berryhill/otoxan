// Package config loads otoxan configuration from YAML and environment variables.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ------------------------------------------------------------------
// Env namespace
// ------------------------------------------------------------------

// All otoxan configuration keys are prefixed with OTOXAN_ to avoid collisions
// with the legacy Hermes environment during shadow-mode operation.
const envPrefix = "OTOXAN_"

// ------------------------------------------------------------------
// Config structs
// ------------------------------------------------------------------

// Config is the top-level otoxan configuration.
type Config struct {
	// DefaultAgent is the agent ID used when none is explicitly specified.
	DefaultAgent string `yaml:"default_agent"`

	// MongoURI is the MongoDB connection string. If empty, Infisical is consulted.
	MongoURI string `yaml:"mongo_uri"`

	// MongoDB is the database name. Defaults to "otoxan".
	MongoDB string `yaml:"mongo_db"`

	// Infisical holds Infisical secret-manager settings.
	Infisical InfisicalConfig `yaml:"infisical"`

	// Agents is a map of agent-name -> per-agent overrides.
	Agents map[string]AgentConfig `yaml:"agents"`

	// StrictMode rejects unknown env-var keys when true.
	StrictMode bool `yaml:"strict_mode"`
}

// InfisicalConfig configures the Infisical secret manager.
type InfisicalConfig struct {
	BaseURL   string `yaml:"base_url"`
	Token     string `yaml:"token"`
	ProjectID string `yaml:"project_id"`
	Env       string `yaml:"env"`
}

// AgentConfig holds per-agent overrides.
type AgentConfig struct {
	ProfilePath string `yaml:"profile_path"`
	Role        string `yaml:"role"`
}

// ------------------------------------------------------------------
// Defaults
// ------------------------------------------------------------------

// Default returns a zero-value Config with sensible defaults applied.
func Default() *Config {
	return &Config{
		DefaultAgent: "default",
		MongoDB:      "otoxan",
		Infisical: InfisicalConfig{
			BaseURL: "https://app.infisical.com",
			Env:     "dev",
		},
		Agents: make(map[string]AgentConfig),
	}
}

// ------------------------------------------------------------------
// Load
// ------------------------------------------------------------------

// Load reads $home/config.yaml (if present) then overlays OTOXAN_* env vars.
// Env vars always win over the file. If the file is missing, defaults are used
// with a warning logged by the caller.
func Load(home string) (*Config, error) {
	cfg := Default()

	if home != "" {
		path := filepath.Join(home, "config.yaml")
		if data, err := os.ReadFile(path); err == nil {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	}

	if err := overlayEnv(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// ------------------------------------------------------------------
// Env overlay
// ------------------------------------------------------------------

// overlayEnv maps OTOXAN_* environment variables onto cfg fields.
// Supported keys:
//
//	OTOXAN_DEFAULT_AGENT   -> cfg.DefaultAgent
//	OTOXAN_MONGO_URI       -> cfg.MongoURI
//	OTOXAN_MONGO_DB        -> cfg.MongoDB
//	OTOXAN_INFISICAL_BASE_URL -> cfg.Infisical.BaseURL
//	OTOXAN_INFISICAL_TOKEN    -> cfg.Infisical.Token
//	OTOXAN_INFISICAL_PROJECT_ID -> cfg.Infisical.ProjectID
//	OTOXAN_INFISICAL_ENV      -> cfg.Infisical.Env
//	OTOXAN_STRICT_MODE        -> cfg.StrictMode ("true"/"1" vs anything else)
//
// In strict mode, unknown OTOXAN_* keys cause an error.
func overlayEnv(cfg *Config) error {
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, envPrefix) {
			continue
		}
		key, val, _ := strings.Cut(e, "=")
		switch key {
		case "OTOXAN_DEFAULT_AGENT":
			cfg.DefaultAgent = val
		case "OTOXAN_MONGO_URI":
			cfg.MongoURI = val
		case "OTOXAN_MONGO_DB":
			cfg.MongoDB = val
		case "OTOXAN_INFISICAL_BASE_URL":
			cfg.Infisical.BaseURL = val
		case "OTOXAN_INFISICAL_TOKEN":
			cfg.Infisical.Token = val
		case "OTOXAN_INFISICAL_PROJECT_ID":
			cfg.Infisical.ProjectID = val
		case "OTOXAN_INFISICAL_ENV":
			cfg.Infisical.Env = val
		case "OTOXAN_STRICT_MODE":
			cfg.StrictMode = val == "true" || val == "1"
		default:
			if cfg.StrictMode {
				return fmt.Errorf("unknown env key %s (strict mode enabled)", key)
			}
		}
	}
	return nil
}
