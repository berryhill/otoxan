package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

func overrideChromeDir(t *testing.T, dir string) func() {
	t.Helper()
	old := chromeNativeMessagingDirFunc
	chromeNativeMessagingDirFunc = func() string { return dir }
	return func() { chromeNativeMessagingDirFunc = old }
}

// chromeNativeMessagingDirFunc is declared in main.go; we reference it here for test overrides.
// init() ensures the default function is wired.
func init() {
	chromeNativeMessagingDirFunc = defaultChromeNativeMessagingDir
}

// ------------------------------------------------------------------
// Linux path resolution
// ------------------------------------------------------------------

func TestChromeNativeMessagingDir_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux-only test")
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "google-chrome", "NativeMessagingHosts")
	got := chromeNativeMessagingDir()
	if got != want {
		t.Errorf("chromeNativeMessagingDir() = %q, want %q", got, want)
	}
}

// ------------------------------------------------------------------
// Install: idempotent overwrite + correct content
// ------------------------------------------------------------------

func TestInstallManifest_Idempotent(t *testing.T) {
	dir := t.TempDir()
	defer overrideChromeDir(t, dir)()

	// First install
	if err := installManifest("abc123"); err != nil {
		t.Fatalf("first install: %v", err)
	}

	path := filepath.Join(dir, manifestName)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest after first install: %v", err)
	}

	// Second install (idempotent overwrite)
	if err := installManifest("abc123"); err != nil {
		t.Fatalf("second install: %v", err)
	}

	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest after second install: %v", err)
	}

	if string(first) != string(second) {
		t.Error("manifest content changed across idempotent installs")
	}

	var m map[string]any
	if err := json.Unmarshal(second, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}

	if m["name"] != "com.otoxan.companion" {
		t.Errorf("name = %q, want %q", m["name"], "com.otoxan.companion")
	}
	if m["type"] != "stdio" {
		t.Errorf("type = %q, want stdio", m["type"])
	}
	if m["description"] != "Otoxan Companion Native Host" {
		t.Errorf("description = %q", m["description"])
	}

	// path must be absolute and point to the test binary
	pathVal, ok := m["path"].(string)
	if !ok || pathVal == "" {
		t.Fatalf("path missing or empty")
	}
	if !filepath.IsAbs(pathVal) {
		t.Errorf("path not absolute: %q", pathVal)
	}

	origins, ok := m["allowed_origins"].([]any)
	if !ok {
		t.Fatalf("allowed_origins type %T", m["allowed_origins"])
	}
	if len(origins) != 1 {
		t.Fatalf("allowed_origins len = %d, want 1", len(origins))
	}
	if origins[0] != "chrome-extension://abc123/" {
		t.Errorf("allowed_origins[0] = %q", origins[0])
	}
}

// ------------------------------------------------------------------
// Install without extension ID
// ------------------------------------------------------------------

func TestInstallManifest_NoExtID(t *testing.T) {
	dir := t.TempDir()
	defer overrideChromeDir(t, dir)()

	if err := installManifest(""); err != nil {
		t.Fatalf("install: %v", err)
	}

	path := filepath.Join(dir, manifestName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}

	origins, ok := m["allowed_origins"].([]any)
	if !ok {
		t.Fatalf("allowed_origins type %T", m["allowed_origins"])
	}
	if len(origins) != 0 {
		t.Errorf("allowed_origins len = %d, want 0", len(origins))
	}
}

// ------------------------------------------------------------------
// Uninstall: removes file, no error when absent
// ------------------------------------------------------------------

func TestUninstallManifest_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	defer overrideChromeDir(t, dir)()

	// Install first
	if err := installManifest("abc123"); err != nil {
		t.Fatalf("install: %v", err)
	}

	path := filepath.Join(dir, manifestName)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("manifest not found before uninstall: %v", err)
	}

	// Uninstall
	if err := uninstallManifest(); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("manifest still exists after uninstall")
	}
}

func TestUninstallManifest_Idempotent(t *testing.T) {
	dir := t.TempDir()
	defer overrideChromeDir(t, dir)()

	// Uninstall when never installed
	if err := uninstallManifest(); err != nil {
		t.Fatalf("uninstall on absent manifest: %v", err)
	}
}
