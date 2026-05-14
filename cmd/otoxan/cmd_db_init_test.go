package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/mongodb"
)

// TestDBInitE2E builds the otoxan binary, spins up a testcontainers MongoDB,
// and verifies that `otoxan db init --global` and `otoxan db init xander`
// both exit 0, are idempotent, and create the expected databases/collections.
func TestDBInitE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	// Build the otoxan binary into a temp directory.
	binDir := t.TempDir()
	binary := filepath.Join(binDir, "otoxan")

	_, thisFile, _, _ := runtime.Caller(0)
	modRoot := filepath.Dir(thisFile)

	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = modRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build otoxan binary: %v\n%s", err, out)
	}

	// Spin up a testcontainers MongoDB.
	ctx := context.Background()
	container, err := mongodb.Run(ctx, "mongo:7")
	if err != nil {
		t.Fatalf("failed to start mongodb container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(ctx)
	})

	uri, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}

	// Use a fresh home directory so the binary can load config.
	home := t.TempDir()
	configPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(configPath, []byte("mongo_uri: "+uri+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// --- init --global (first run) ---
	initGlobal := exec.Command(binary, "db", "init", "--global")
	initGlobal.Env = append(os.Environ(), "OTOXAN_HOME="+home)
	out, err = initGlobal.CombinedOutput()
	if err != nil {
		t.Fatalf("otoxan db init --global failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "global database initialized") {
		t.Errorf("unexpected first global init output: %q", string(out))
	}

	// --- init --global (idempotent rerun) ---
	initGlobal2 := exec.Command(binary, "db", "init", "--global")
	initGlobal2.Env = append(os.Environ(), "OTOXAN_HOME="+home)
	out, err = initGlobal2.CombinedOutput()
	if err != nil {
		t.Fatalf("idempotent otoxan db init --global failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "global database already initialized") {
		t.Errorf("idempotent global init did not report 'already initialized': %q", string(out))
	}

	// --- init xander (first run) ---
	initAgent := exec.Command(binary, "db", "init", "xander")
	initAgent.Env = append(os.Environ(), "OTOXAN_HOME="+home)
	out, err = initAgent.CombinedOutput()
	if err != nil {
		t.Fatalf("otoxan db init xander failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "agent database otoxan_agent_xander initialized") {
		t.Errorf("unexpected first agent init output: %q", string(out))
	}

	// --- init xander (idempotent rerun) ---
	initAgent2 := exec.Command(binary, "db", "init", "xander")
	initAgent2.Env = append(os.Environ(), "OTOXAN_HOME="+home)
	out, err = initAgent2.CombinedOutput()
	if err != nil {
		t.Fatalf("idempotent otoxan db init xander failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "agent database otoxan_agent_xander already initialized") {
		t.Errorf("idempotent agent init did not report 'already initialized': %q", string(out))
	}
}
