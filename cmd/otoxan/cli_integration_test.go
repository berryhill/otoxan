package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/silas/otoxan/internal/firstrun"
)

var testBinary string

func TestMain(m *testing.M) {
	// Build the otoxan binary once for all integration tests.
	_, thisFile, _, _ := runtime.Caller(0)
	modRoot := filepath.Dir(thisFile)
	binDir, err := os.MkdirTemp("", "otoxan-integ-*")
	if err != nil {
		panic(err)
	}
	testBinary = filepath.Join(binDir, "otoxan")
	cmd := exec.Command("go", "build", "-o", testBinary, ".")
	cmd.Dir = modRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		panic(string(out))
	}
	code := m.Run()
	_ = os.RemoveAll(binDir)
	os.Exit(code)
}

// TestIntegration_FreshHomeOnboarding runs otoxan with no args in a fresh
// home directory and asserts the onboarding flow is selected.
func TestIntegration_FreshHomeOnboarding(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	home := t.TempDir()

	// Provide "exit" as the first input so the REPL ends immediately.
	stdin := strings.NewReader("exit\n")

	cmd := exec.Command(testBinary)
	cmd.Env = append(os.Environ(), "OTOXAN_HOME="+home)
	cmd.Stdin = stdin

	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		// Non-zero exit is fine — the REPL may exit with code 1 on EOF or error.
		t.Logf("binary exited with error: %v", err)
	}

	combined := out.String() + errOut.String()
	t.Logf("output:\n%s", combined)

	// Assert the session banner mentions onboarding flow.
	if !strings.Contains(combined, "flow: onboarding") {
		t.Errorf("expected 'flow: onboarding' in output, got:\n%s", combined)
	}
}

// TestIntegration_ExistingHomeDefault runs otoxan with no args after the
// onboarding sentinel has been written and asserts the default flow is selected.
func TestIntegration_ExistingHomeDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	home := t.TempDir()
	if err := firstrun.MarkOnboardingComplete(home); err != nil {
		t.Fatalf("mark onboarding complete: %v", err)
	}

	stdin := strings.NewReader("exit\n")

	cmd := exec.Command(testBinary)
	cmd.Env = append(os.Environ(), "OTOXAN_HOME="+home)
	cmd.Stdin = stdin

	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	if err := cmd.Run(); err != nil {
		t.Logf("binary exited with error: %v", err)
	}

	combined := out.String() + errOut.String()
	t.Logf("output:\n%s", combined)

	if !strings.Contains(combined, "flow: default") {
		t.Errorf("expected 'flow: default' in output, got:\n%s", combined)
	}
}

// TestIntegration_CrashResume writes three turns into a session, simulates a
// process kill, then re-opens the session and asserts replayTail returns all
// three turns in chronological order.
func TestIntegration_CrashResume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	home := t.TempDir()
	if err := firstrun.MarkOnboardingComplete(home); err != nil {
		t.Fatalf("mark onboarding complete: %v", err)
	}

	// First invocation: send three messages then EOF.
	stdin1 := strings.NewReader("one\ntwo\nthree\n")
	cmd1 := exec.Command(testBinary)
	cmd1.Env = append(os.Environ(), "OTOXAN_HOME="+home)
	cmd1.Stdin = stdin1

	var out1 bytes.Buffer
	cmd1.Stdout = &out1
	cmd1.Stderr = &out1

	// Start the process.
	if err := cmd1.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}

	// Give it time to process the three lines, then kill it.
	time.Sleep(500 * time.Millisecond)
	_ = cmd1.Process.Kill()
	_, _ = cmd1.Process.Wait()

	// Second invocation: reopen the same session with EOF to trigger replay.
	stdin2 := strings.NewReader("")
	cmd2 := exec.Command(testBinary)
	cmd2.Env = append(os.Environ(), "OTOXAN_HOME="+home)
	cmd2.Stdin = stdin2

	var out2 bytes.Buffer
	cmd2.Stdout = &out2
	cmd2.Stderr = &out2

	if err := cmd2.Run(); err != nil {
		t.Logf("binary exited with error: %v", err)
	}

	combined := out2.String()
	t.Logf("resume output:\n%s", combined)

	// The resumed session should have loaded the existing open session.
	// We verify by checking that the banner still shows the same session
	// (it will resume the most-recent open session).  The key assertion is
	// that the session does not create a brand-new one — the flow_id in DB
	// should still be "default" and the turns table should contain the prior
	// user turns.
	if !strings.Contains(combined, "flow: default") {
		t.Errorf("expected 'flow: default' in resume output, got:\n%s", combined)
	}

	// Verify turns exist in state.db by querying it directly.
	dbPath := filepath.Join(home, "state.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatalf("state.db should exist after first run")
	}

	// Wait for the DB to be fully written before querying.
	time.Sleep(200 * time.Millisecond)

	// Use sqlite3 CLI to count user turns.
	queryCmd := exec.Command("sqlite3", dbPath, "SELECT COUNT(*) FROM turns WHERE role='user';")
	queryOut, err := queryCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("query state.db: %v\n%s", err, queryOut)
	}
	count := strings.TrimSpace(string(queryOut))
	if count != "3" {
		t.Errorf("expected 3 user turns in DB, got %s", count)
	}

	// Verify the turn bodies are in order.
	queryCmd = exec.Command("sqlite3", dbPath, "SELECT body FROM turns WHERE role='user' ORDER BY created_at;")
	queryOut, err = queryCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("query state.db: %v\n%s", err, queryOut)
	}
	bodies := strings.Split(strings.TrimSpace(string(queryOut)), "\n")
	if len(bodies) != 3 || bodies[0] != "one" || bodies[1] != "two" || bodies[2] != "three" {
		t.Errorf("expected turns [one two three], got %v", bodies)
	}
}
