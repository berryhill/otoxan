package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.DefaultAgent != "default" {
		t.Fatalf("expected default_agent=default, got %s", cfg.DefaultAgent)
	}
	if cfg.MongoDB != "otoxan" {
		t.Fatalf("expected mongo_db=otoxan, got %s", cfg.MongoDB)
	}
	if cfg.Infisical.BaseURL != "https://app.infisical.com" {
		t.Fatalf("expected infisical.base_url default, got %s", cfg.Infisical.BaseURL)
	}
	if cfg.Infisical.Env != "dev" {
		t.Fatalf("expected infisical.env=dev, got %s", cfg.Infisical.Env)
	}
}

func TestLoadFromFile(t *testing.T) {
	tmp := t.TempDir()
	content := `
default_agent: alice
mongo_uri: mongodb://localhost:27017/test
mongo_db: testdb
strict_mode: true
infisical:
  base_url: https://infisical.example.com
  token: tok-123
  project_id: proj-456
  env: staging
agents:
  alice:
    profile_path: /home/alice/.otoxan/profiles/alice
    role: admin
`
	if err := os.WriteFile(filepath.Join(tmp, "config.yaml"), []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultAgent != "alice" {
		t.Fatalf("expected default_agent=alice, got %s", cfg.DefaultAgent)
	}
	if cfg.MongoURI != "mongodb://localhost:27017/test" {
		t.Fatalf("expected mongo_uri, got %s", cfg.MongoURI)
	}
	if cfg.MongoDB != "testdb" {
		t.Fatalf("expected mongo_db=testdb, got %s", cfg.MongoDB)
	}
	if !cfg.StrictMode {
		t.Fatal("expected strict_mode=true")
	}
	if cfg.Infisical.Token != "tok-123" {
		t.Fatalf("expected token, got %s", cfg.Infisical.Token)
	}
	if cfg.Infisical.ProjectID != "proj-456" {
		t.Fatalf("expected project_id, got %s", cfg.Infisical.ProjectID)
	}
	alice, ok := cfg.Agents["alice"]
	if !ok {
		t.Fatal("expected agents.alice")
	}
	if alice.ProfilePath != "/home/alice/.otoxan/profiles/alice" {
		t.Fatalf("expected profile_path, got %s", alice.ProfilePath)
	}
}

func TestLoadMissingFile(t *testing.T) {
	tmp := t.TempDir()
	// No config.yaml written
	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if cfg.DefaultAgent != "default" {
		t.Fatalf("expected defaults when file missing, got default_agent=%s", cfg.DefaultAgent)
	}
}

func TestEnvOverlay(t *testing.T) {
	tmp := t.TempDir()
	content := `
default_agent: alice
mongo_uri: mongodb://localhost:27017/test
`
	if err := os.WriteFile(filepath.Join(tmp, "config.yaml"), []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	os.Setenv("OTOXAN_DEFAULT_AGENT", "bob")
	os.Setenv("OTOXAN_MONGO_URI", "mongodb://envhost:27017/envdb")
	os.Setenv("OTOXAN_MONGO_DB", "envdb")
	os.Setenv("OTOXAN_INFISICAL_TOKEN", "env-token")
	defer os.Unsetenv("OTOXAN_DEFAULT_AGENT")
	defer os.Unsetenv("OTOXAN_MONGO_URI")
	defer os.Unsetenv("OTOXAN_MONGO_DB")
	defer os.Unsetenv("OTOXAN_INFISICAL_TOKEN")

	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultAgent != "bob" {
		t.Fatalf("env should win: expected bob, got %s", cfg.DefaultAgent)
	}
	if cfg.MongoURI != "mongodb://envhost:27017/envdb" {
		t.Fatalf("env should win: expected env mongo_uri, got %s", cfg.MongoURI)
	}
	if cfg.MongoDB != "envdb" {
		t.Fatalf("expected envdb, got %s", cfg.MongoDB)
	}
	if cfg.Infisical.Token != "env-token" {
		t.Fatalf("expected env-token, got %s", cfg.Infisical.Token)
	}
}

func TestStrictModeRejectsUnknown(t *testing.T) {
	os.Setenv("OTOXAN_STRICT_MODE", "true")
	os.Setenv("OTOXAN_UNKNOWN_KEY", "nope")
	defer os.Unsetenv("OTOXAN_STRICT_MODE")
	defer os.Unsetenv("OTOXAN_UNKNOWN_KEY")

	_, err := Load("")
	if err == nil {
		t.Fatal("expected error for unknown key in strict mode")
	}
}

func TestStrictModeAllowsKnown(t *testing.T) {
	os.Setenv("OTOXAN_STRICT_MODE", "true")
	os.Setenv("OTOXAN_DEFAULT_AGENT", "charlie")
	defer os.Unsetenv("OTOXAN_STRICT_MODE")
	defer os.Unsetenv("OTOXAN_DEFAULT_AGENT")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultAgent != "charlie" {
		t.Fatalf("expected charlie, got %s", cfg.DefaultAgent)
	}
}

func TestNonStrictModeIgnoresUnknown(t *testing.T) {
	os.Setenv("OTOXAN_UNKNOWN_KEY", "nope")
	defer os.Unsetenv("OTOXAN_UNKNOWN_KEY")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultAgent != "default" {
		t.Fatalf("expected default agent, got %s", cfg.DefaultAgent)
	}
}

func TestEnvHomeResolution(t *testing.T) {
	// Verify Load uses the provided home path
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "config.yaml"), []byte("default_agent: dave\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(tmp)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultAgent != "dave" {
		t.Fatalf("expected dave, got %s", cfg.DefaultAgent)
	}
}
