package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/silas/otoxan/internal/install"
)

// TestInitE2E builds the otoxan binary, runs `otoxan init` with OTOXAN_HOME
// pointing at a fresh temp directory, and asserts the canonical layout exists
// with correct file modes.
func TestInitE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	// Build the otoxan binary into a temp directory.
	binDir := t.TempDir()
	binary := filepath.Join(binDir, "otoxan")

	// Determine the module root (one level up from cmd/otoxan).
	_, thisFile, _, _ := runtime.Caller(0)
	modRoot := filepath.Dir(thisFile)

	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = modRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build otoxan binary: %v\n%s", err, out)
	}

	// Run `otoxan init` with a fresh home directory.
	home := t.TempDir()
	initCmd := exec.Command(binary, "init")
	initCmd.Env = append(os.Environ(), "OTOXAN_HOME="+home)
	out, err = initCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("otoxan init failed: %v\n%s", err, out)
	}

	// Verify output mentions initialization.
	if !strings.Contains(string(out), "Initialized otoxan") && !strings.Contains(string(out), "already initialized") {
		t.Errorf("unexpected init output: %q", string(out))
	}

	// Assert canonical layout.
	lay := install.DirLayout(home)

	for _, d := range []string{lay.Home, lay.Bin, lay.Logs, lay.Cache} {
		fi, err := os.Stat(d)
		if err != nil {
			t.Errorf("expected dir %s to exist: %v", d, err)
			continue
		}
		if !fi.IsDir() {
			t.Errorf("expected %s to be a directory", d)
			continue
		}
		mode := fi.Mode().Perm()
		// umask may relax group/other bits; only check owner bits.
		if mode&0o700 != 0o700 {
			t.Errorf("dir %s owner mode = 0o%o, want owner rwx", d, mode)
		}
	}

	// Config stub should exist with mode 0o600.
	if fi, err := os.Stat(lay.Config); err != nil {
		t.Errorf("expected config stub %s to exist: %v", lay.Config, err)
	} else {
		mode := fi.Mode().Perm()
		if mode != 0o600 {
			t.Errorf("config %s mode = 0o%o, want 0o600", lay.Config, mode)
		}
	}

	// Version file should exist with mode 0o644 and contain a version.
	if fi, err := os.Stat(lay.Version); err != nil {
		t.Errorf("expected version file %s to exist: %v", lay.Version, err)
	} else {
		mode := fi.Mode().Perm()
		if mode != 0o644 {
			t.Errorf("version %s mode = 0o%o, want 0o644", lay.Version, mode)
		}
	}

	ver, err := install.ReadVersion(home)
	if err != nil {
		t.Fatalf("ReadVersion() error = %v", err)
	}
	if ver == "" {
		t.Error("expected non-empty version in version file")
	}

	// Idempotent re-run should succeed and report already initialized.
	initCmd2 := exec.Command(binary, "init")
	initCmd2.Env = append(os.Environ(), "OTOXAN_HOME="+home)
	out2, err := initCmd2.CombinedOutput()
	if err != nil {
		t.Fatalf("idempotent otoxan init failed: %v\n%s", err, out2)
	}
	if !strings.Contains(string(out2), "already initialized") {
		t.Errorf("idempotent init did not report 'already initialized': %q", string(out2))
	}
}
