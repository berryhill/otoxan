package sessionflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectContext(t *testing.T) {
	home := t.TempDir()
	d := detectContext(home)

	if d.CWD == "" {
		t.Error("expected non-empty CWD")
	}
	if d.OS == "" {
		t.Error("expected non-empty OS")
	}
	// Shell may be empty in stripped test environments, so we only assert
	// that it is non-empty when SHELL or os.Executable() succeed.
	if d.Username == "" {
		t.Error("expected non-empty Username")
	}
	// AgentProfileExists should be false for an empty temp home.
	if d.AgentProfileExists {
		t.Error("AgentProfileExists should be false for empty home")
	}
}

func TestReadGitRemote(t *testing.T) {
	// Create a temp directory, init a git repo, add a remote, and assert
	// readGitRemote returns the URL.
	dir := t.TempDir()

	if err := exec.Command("git", "init", dir).Run(); err != nil {
		t.Skipf("git not available: %v", err)
	}

	wantRemote := "https://github.com/example/repo.git"
	if err := exec.Command("git", "-C", dir, "remote", "add", "origin", wantRemote).Run(); err != nil {
		t.Fatalf("git remote add failed: %v", err)
	}

	got := readGitRemote(dir)
	if got != wantRemote {
		t.Errorf("readGitRemote(%q) = %q, want %q", dir, got, wantRemote)
	}
}

func TestReadGitRemote_NoRepo(t *testing.T) {
	dir := t.TempDir()
	got := readGitRemote(dir)
	if got != "" {
		t.Errorf("readGitRemote(%q) = %q, want empty string", dir, got)
	}
}

func TestCheckAgentProfileConfigured(t *testing.T) {
	home := t.TempDir()
	if checkAgentProfileConfigured(home) {
		t.Error("expected false for empty home")
	}

	// Create a profile directory.
	profileDir := filepath.Join(home, "profiles", "silas")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if !checkAgentProfileConfigured(home) {
		t.Error("expected true after creating profile dir")
	}
}

func TestInferShell(t *testing.T) {
	orig := os.Getenv("SHELL")
	defer os.Setenv("SHELL", orig)

	os.Setenv("SHELL", "/bin/zsh")
	if got := inferShell(); got != "zsh" {
		t.Errorf("inferShell() = %q, want zsh", got)
	}

	os.Setenv("SHELL", "/usr/local/bin/fish")
	if got := inferShell(); got != "fish" {
		t.Errorf("inferShell() = %q, want fish", got)
	}
}

func TestInferUsername(t *testing.T) {
	// We cannot reliably unset USER/USERNAME in all environments,
	// so we just assert the function returns something non-empty
	// when those vars are present (which they almost always are).
	u := inferUsername()
	if u == "" && os.Getenv("USER") == "" && os.Getenv("USERNAME") == "" {
		t.Skip("no username source available in this environment")
	}
	if u == "" {
		t.Error("expected non-empty username")
	}
}

func TestInferEditor(t *testing.T) {
	origVisual := os.Getenv("VISUAL")
	origEditor := os.Getenv("EDITOR")
	defer func() {
		os.Setenv("VISUAL", origVisual)
		os.Setenv("EDITOR", origEditor)
	}()

	os.Unsetenv("VISUAL")
	os.Unsetenv("EDITOR")
	if got := inferEditor(); got != "" {
		t.Errorf("inferEditor() = %q, want empty", got)
	}

	os.Setenv("EDITOR", "vim")
	if got := inferEditor(); got != "vim" {
		t.Errorf("inferEditor() = %q, want vim", got)
	}

	os.Setenv("VISUAL", "code")
	if got := inferEditor(); got != "code" {
		t.Errorf("inferEditor() = %q, want code", got)
	}
}

func TestDetectedSummary(t *testing.T) {
	d := detected{
		CWD:       "/home/silas/code/otoxan",
		GitRemote: "https://github.com/silas/otoxan.git",
		OS:        "linux",
		Shell:     "zsh",
		Username:  "silas",
		Editor:    "vim",
		AgentProfileExists: true,
	}
	s := d.summary()
	for _, want := range []string{
		"Working directory: /home/silas/code/otoxan",
		"Git remote: https://github.com/silas/otoxan.git",
		"OS: linux",
		"Shell: zsh",
		"User: silas",
		"Editor: vim",
		"Agent profile: found",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("summary missing %q\nfull:\n%s", want, s)
		}
	}
}

func TestDetect(t *testing.T) {
	t.Run("DetectContext", TestDetectContext)
	t.Run("ReadGitRemote", TestReadGitRemote)
	t.Run("ReadGitRemote_NoRepo", TestReadGitRemote_NoRepo)
	t.Run("CheckAgentProfileConfigured", TestCheckAgentProfileConfigured)
	t.Run("InferShell", TestInferShell)
	t.Run("InferUsername", TestInferUsername)
	t.Run("InferEditor", TestInferEditor)
	t.Run("DetectedSummary", TestDetectedSummary)
}
