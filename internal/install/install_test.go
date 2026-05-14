package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHome(t *testing.T) {
	// Save and restore env
	origOTOXAN := os.Getenv("OTOXAN_HOME")
	origHOME := os.Getenv("HOME")
	defer func() {
		os.Setenv("OTOXAN_HOME", origOTOXAN)
		os.Setenv("HOME", origHOME)
	}()

	t.Run("OTOXAN_HOME_set", func(t *testing.T) {
		os.Setenv("OTOXAN_HOME", "/custom/otoxan")
		os.Unsetenv("HOME")
		got := Home()
		want := "/custom/otoxan"
		if got != want {
			t.Errorf("Home() = %q, want %q", got, want)
		}
	})

	t.Run("OTOXAN_HOME_unset", func(t *testing.T) {
		os.Unsetenv("OTOXAN_HOME")
		os.Setenv("HOME", "/home/alice")
		got := Home()
		want := filepath.Join("/home/alice", ".otoxan")
		if got != want {
			t.Errorf("Home() = %q, want %q", got, want)
		}
	})

	t.Run("OTOXAN_HOME_empty", func(t *testing.T) {
		os.Setenv("OTOXAN_HOME", "")
		os.Setenv("HOME", "/home/bob")
		got := Home()
		want := filepath.Join("/home/bob", ".otoxan")
		if got != want {
			t.Errorf("Home() = %q, want %q", got, want)
		}
	})
}

func TestInit(t *testing.T) {
	t.Run("fresh_init", func(t *testing.T) {
		tmp := t.TempDir()
		home := filepath.Join(tmp, "fresh")

		if err := Init(home, false, "0.1.0"); err != nil {
			t.Fatalf("Init() error = %v", err)
		}

		lay := DirLayout(home)
		for _, d := range []string{lay.Home, lay.Bin, lay.Logs, lay.Cache} {
			if fi, err := os.Stat(d); err != nil {
				t.Errorf("expected dir %s to exist: %v", d, err)
			} else if !fi.IsDir() {
				t.Errorf("expected %s to be a directory", d)
			}
		}

		// Config stub should exist.
		if data, err := os.ReadFile(lay.Config); err != nil {
			t.Errorf("expected config stub: %v", err)
		} else if string(data) == "" {
			t.Error("expected non-empty config stub")
		}

		// Version file should contain the version.
		ver, err := ReadVersion(home)
		if err != nil {
			t.Fatalf("ReadVersion() error = %v", err)
		}
		if ver != "0.1.0" {
			t.Errorf("ReadVersion() = %q, want %q", ver, "0.1.0")
		}
	})

	t.Run("idempotent_reinit", func(t *testing.T) {
		tmp := t.TempDir()
		home := filepath.Join(tmp, "idempotent")

		if err := Init(home, false, "0.1.0"); err != nil {
			t.Fatalf("Init() error = %v", err)
		}

		// Write custom config.
		lay := DirLayout(home)
		customConfig := "custom: true\n"
		if err := os.WriteFile(lay.Config, []byte(customConfig), 0o600); err != nil {
			t.Fatalf("write custom config: %v", err)
		}

		// Re-init with new version.
		if err := Init(home, false, "0.2.0"); err != nil {
			t.Fatalf("Init() error = %v", err)
		}

		// Config should be preserved.
		data, err := os.ReadFile(lay.Config)
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		if string(data) != customConfig {
			t.Errorf("config overwritten: got %q, want %q", string(data), customConfig)
		}

		// Version should be updated.
		ver, err := ReadVersion(home)
		if err != nil {
			t.Fatalf("ReadVersion() error = %v", err)
		}
		if ver != "0.2.0" {
			t.Errorf("ReadVersion() = %q, want %q", ver, "0.2.0")
		}
	})

	t.Run("force_preserves_config", func(t *testing.T) {
		tmp := t.TempDir()
		home := filepath.Join(tmp, "force")

		if err := Init(home, false, "0.1.0"); err != nil {
			t.Fatalf("Init() error = %v", err)
		}

		// Write custom config.
		lay := DirLayout(home)
		customConfig := "force_test: true\n"
		if err := os.WriteFile(lay.Config, []byte(customConfig), 0o600); err != nil {
			t.Fatalf("write custom config: %v", err)
		}

		// Force re-init.
		if err := Init(home, true, "0.3.0"); err != nil {
			t.Fatalf("Init() error = %v", err)
		}

		// Config should still be preserved.
		data, err := os.ReadFile(lay.Config)
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		if string(data) != customConfig {
			t.Errorf("config overwritten despite force=true: got %q, want %q", string(data), customConfig)
		}

		// Version should be updated.
		ver, err := ReadVersion(home)
		if err != nil {
			t.Fatalf("ReadVersion() error = %v", err)
		}
		if ver != "0.3.0" {
			t.Errorf("ReadVersion() = %q, want %q", ver, "0.3.0")
		}
	})
}

// makeFakeBinary creates an executable-looking file for use as a fake binary.
func makeFakeBinary(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
}

// fakeGitHubServer spins up an httptest server that serves a latest-release
// JSON and a single asset tarball. The tarball is just raw bytes (not a real
// tar.gz) so we can feed it straight into Update as the new binary.
func fakeGitHubServer(t *testing.T, version string, assetBytes []byte) (*httptest.Server, *GitHubClient) {
	t.Helper()

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)

	rel := Release{
		TagName: "v" + version,
		Assets: []Asset{
			{
				Name: fmt.Sprintf("otoxan_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH),
				URL:  server.URL + "/asset",
			},
		},
	}

	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(rel); err != nil {
			t.Fatalf("encode release: %v", err)
		}
	})

	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		if _, err := io.Copy(w, bytes.NewReader(assetBytes)); err != nil {
			t.Fatalf("serve asset: %v", err)
		}
	})

	client := NewGitHubClient("owner", "repo")
	client.BaseURL = server.URL
	client.HTTPClient = server.Client()

	return server, client
}

// fakeGitHubServerNoAsset serves a release but 404s on the asset download.
func fakeGitHubServerNoAsset(t *testing.T, version string) (*httptest.Server, *GitHubClient) {
	t.Helper()
		// Server that serves a release but then 404s on the asset.
		var srv *httptest.Server
		srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/repos/owner/repo/releases/latest" {
				rel := Release{
					TagName: "v" + version,
					Assets: []Asset{
						{
							Name: fmt.Sprintf("otoxan_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH),
							URL:  srv.URL + "/asset",
						},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(rel); err != nil {
					t.Fatalf("encode release: %v", err)
				}
				return
			}
			if r.URL.Path == "/asset" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))

		client := NewGitHubClient("owner", "repo")
		client.BaseURL = srv.URL
		client.HTTPClient = srv.Client()

		return srv, client
	}

func TestConfig(t *testing.T) {
	t.Run("default_write", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "config.yaml")

		if err := WriteDefaultConfig(path); err != nil {
			t.Fatalf("WriteDefaultConfig() error = %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read config: %v", err)
		}
		if string(data) != DefaultConfigYAML {
			t.Errorf("default config mismatch\ngot:\n%s\nwant:\n%s", string(data), DefaultConfigYAML)
		}
	})

	t.Run("user_edit_survives_merge", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "config.yaml")

		// Write a user-edited config that omits some keys and overrides others.
		userConfig := `# my custom header
mongo_uri: mongodb://myhost
mongo_db: mydb
strict_mode: true
`
		if err := os.WriteFile(path, []byte(userConfig), 0o600); err != nil {
			t.Fatalf("write user config: %v", err)
		}

		// Merge with current defaults.
		if err := MergeConfig(path, []byte(DefaultConfigYAML)); err != nil {
			t.Fatalf("MergeConfig() error = %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read merged config: %v", err)
		}
		merged := string(data)

		// User edits must survive.
		if !strings.Contains(merged, "mongo_uri: mongodb://myhost") {
			t.Error("user mongo_uri edit was lost")
		}
		if !strings.Contains(merged, "mongo_db: mydb") {
			t.Error("user mongo_db edit was lost")
		}
		if !strings.Contains(merged, "strict_mode: true") {
			t.Error("user strict_mode edit was lost")
		}
		if !strings.Contains(merged, "# my custom header") {
			t.Error("user comment/header was lost")
		}
	})

	t.Run("new_keys_appended_as_comments", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "config.yaml")

		// User config missing several keys that exist in defaults.
		userConfig := `mongo_uri: mongodb://host
`
		if err := os.WriteFile(path, []byte(userConfig), 0o600); err != nil {
			t.Fatalf("write user config: %v", err)
		}

		// Merge with current defaults.
		if err := MergeConfig(path, []byte(DefaultConfigYAML)); err != nil {
			t.Fatalf("MergeConfig() error = %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read merged config: %v", err)
		}
		merged := string(data)

		// New keys should appear as commented blocks at the end.
		if !strings.Contains(merged, "# --- New keys added by otoxan update") {
			t.Error("missing 'New keys added' header")
		}

		// Check that missing keys are present as comments.
		expectedMissing := []string{
			"# default_agent:",
			"# mongo_db:",
			"# infisical:",
			"# agents:",
			"# strict_mode:",
		}
		for _, want := range expectedMissing {
			if !strings.Contains(merged, want) {
				t.Errorf("missing commented key %q in merged config", want)
			}
		}

		// User's original value must still be present and uncommented.
		if !strings.Contains(merged, "mongo_uri: mongodb://host") {
			t.Error("user mongo_uri value was lost")
		}
	})
}

func TestUpdate(t *testing.T) {
	// Override CurrentBinary to point at a fake binary in a temp dir.
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	currentBinary := filepath.Join(binDir, "otoxan")
	makeFakeBinary(t, currentBinary, "old-binary")

	// Set the package-level override so CurrentBinary() returns our fake path.
	SetCurrentBinaryOverride(currentBinary)
	defer func() { SetCurrentBinaryOverride("") }()

	newBinaryContent := "new-binary"

	t.Run("no_op_when_current", func(t *testing.T) {
		server, client := fakeGitHubServer(t, "0.5.0", []byte(newBinaryContent))
		defer server.Close()

		err := Update("0.5.0", client, nil)
		if err != nil {
			t.Fatalf("Update() error = %v, want nil", err)
		}

		// Current binary should be untouched.
		data, err := os.ReadFile(currentBinary)
		if err != nil {
			t.Fatalf("read current binary: %v", err)
		}
		if string(data) != "old-binary" {
			t.Errorf("binary changed on no-op: got %q, want %q", string(data), "old-binary")
		}
	})

	t.Run("happy_path_swap", func(t *testing.T) {
		// Reset the fake binary to old content.
		makeFakeBinary(t, currentBinary, "old-binary")

		server, client := fakeGitHubServer(t, "0.6.0", []byte(newBinaryContent))
		defer server.Close()

		// Smoke test that just checks the file is readable and non-empty.
		smoke := func(path string) error {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if len(data) == 0 {
				return fmt.Errorf("empty binary")
			}
			return nil
		}

		err := Update("0.5.0", client, smoke)
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}

		// Current binary should now be the new content.
		data, err := os.ReadFile(currentBinary)
		if err != nil {
			t.Fatalf("read current binary: %v", err)
		}
		if string(data) != newBinaryContent {
			t.Errorf("binary not updated: got %q, want %q", string(data), newBinaryContent)
		}

		// .prev backup should contain the old binary.
		prevPath := filepath.Join(binDir, ".otoxan.prev")
		prevData, err := os.ReadFile(prevPath)
		if err != nil {
			t.Fatalf("read .prev: %v", err)
		}
		if string(prevData) != "old-binary" {
			t.Errorf(".prev backup wrong: got %q, want %q", string(prevData), "old-binary")
		}

		// .tmp should be cleaned up.
		tmpPath := filepath.Join(binDir, ".otoxan.tmp")
		if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
			t.Errorf(".tmp file should not exist after successful update")
		}
	})

	t.Run("smoke_test_failure_rolls_back", func(t *testing.T) {
		// Reset the fake binary to old content.
		makeFakeBinary(t, currentBinary, "old-binary")
		// Clean up any leftover .prev from previous sub-test.
		_ = os.Remove(filepath.Join(binDir, ".otoxan.prev"))

		server, client := fakeGitHubServer(t, "0.7.0", []byte(newBinaryContent))
		defer server.Close()

		// Smoke test that always fails.
		failingSmoke := func(path string) error {
			return fmt.Errorf("intentional smoke test failure")
		}

		err := Update("0.5.0", client, failingSmoke)
		if err == nil {
			t.Fatal("Update() error = nil, want smoke-test failure")
		}

		// Current binary should still be the old content.
		data, err := os.ReadFile(currentBinary)
		if err != nil {
			t.Fatalf("read current binary: %v", err)
		}
		if string(data) != "old-binary" {
			t.Errorf("binary changed despite smoke failure: got %q, want %q", string(data), "old-binary")
		}

		// .prev should not exist because backup only happens after smoke passes.
		prevPath := filepath.Join(binDir, ".otoxan.prev")
		if _, err := os.Stat(prevPath); !os.IsNotExist(err) {
			t.Errorf(".prev should not exist when smoke test fails")
		}

		// .tmp should be cleaned up.
		tmpPath := filepath.Join(binDir, ".otoxan.tmp")
		if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
			t.Errorf(".tmp file should not exist after smoke-test failure")
		}
	})

	t.Run("partial_download_cleaned_up_on_error", func(t *testing.T) {
		// Reset the fake binary to old content.
		makeFakeBinary(t, currentBinary, "old-binary")
		_ = os.Remove(filepath.Join(binDir, ".otoxan.prev"))

		server, client := fakeGitHubServerNoAsset(t, "0.8.0")
		defer server.Close()

		err := Update("0.5.0", client, nil)
		if err == nil {
			t.Fatal("Update() error = nil, want download failure")
		}

		// Current binary should be untouched.
		data, err := os.ReadFile(currentBinary)
		if err != nil {
			t.Fatalf("read current binary: %v", err)
		}
		if string(data) != "old-binary" {
			t.Errorf("binary changed on download error: got %q, want %q", string(data), "old-binary")
		}

		// .tmp should not exist.
		tmpPath := filepath.Join(binDir, ".otoxan.tmp")
		if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
			t.Errorf(".tmp file should not exist after failed download")
		}
	})
}
