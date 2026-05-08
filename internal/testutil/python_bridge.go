// Package testutil provides helpers for cross-language parity tests.
package testutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// pythonFixtureHelperPath is the path to the Python bridge script.
const pythonFixtureHelperPath = "scripts/python_fixture_helper.py"

// resolveHelper returns the absolute path to the Python helper.
// It looks relative to the otoxan repo root (detected via go.mod or GOPATH).
func resolveHelper() (string, error) {
	// Try relative to working directory first
	if _, err := os.Stat(pythonFixtureHelperPath); err == nil {
		abs, _ := filepath.Abs(pythonFixtureHelperPath)
		return abs, nil
	}
	// Walk up from current working directory looking for go.mod
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, pythonFixtureHelperPath)
		if _, err := os.Stat(candidate); err == nil {
			abs, _ := filepath.Abs(candidate)
			return abs, nil
		}
		// Also check one level up from internal/ (e.g. internal/store/tasks -> ../../scripts)
		candidate2 := filepath.Join(dir, "..", "..", pythonFixtureHelperPath)
		if _, err := os.Stat(candidate2); err == nil {
			abs, _ := filepath.Abs(candidate2)
			return abs, nil
		}
	}
	return "", fmt.Errorf("cannot find %s from wd %s", pythonFixtureHelperPath, wd)
}

// PythonWriteFixture asks the Python helper to write a minimal fixture document
// into the given store with the specified id, then returns the raw BSON map
// as stored by Python.
func PythonWriteFixture(t *testing.T, store, id string) bson.M {
	t.Helper()
	helper, err := resolveHelper()
	if err != nil {
		t.Fatalf("resolve helper: %v", err)
	}

	cmd := exec.Command("python3", helper, "--store", store, "--op", "write", "--id", id)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+resolvePythonPath())
	// Inherit MONGO_URI from test environment so Python and Go talk to the same DB
	if uri := os.Getenv("MONGO_URI"); uri != "" {
		cmd.Env = append(cmd.Env, "MONGO_URI="+uri)
	}

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("python_fixture_helper.py write crashed (store=%s id=%s): %v\nstderr: %s", store, id, err, stderr.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("python_fixture_helper.py write produced invalid JSON (store=%s id=%s): %v\nstdout: %s", store, id, err, out.String())
	}

	if errMsg, ok := result["error"].(string); ok && errMsg != "" {
		t.Fatalf("python_fixture_helper.py write returned error (store=%s id=%s): %s\nstderr: %s", store, id, errMsg, stderr.String())
	}

	// Convert to bson.M so Go tests can compare directly with mongo-driver results
	m := make(bson.M, len(result))
	for k, v := range result {
		m[k] = v
	}
	return m
}

// PythonReadFixture asks the Python helper to read a document by id from the
// given store and returns the raw BSON map.
func PythonReadFixture(t *testing.T, store, id string) bson.M {
	t.Helper()
	helper, err := resolveHelper()
	if err != nil {
		t.Fatalf("resolve helper: %v", err)
	}

	cmd := exec.Command("python3", helper, "--store", store, "--op", "read", "--id", id)
	cmd.Env = append(os.Environ(), "PYTHONPATH="+resolvePythonPath())
	// Inherit MONGO_URI from test environment so Python and Go talk to the same DB
	if uri := os.Getenv("MONGO_URI"); uri != "" {
		cmd.Env = append(cmd.Env, "MONGO_URI="+uri)
	}

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("python_fixture_helper.py read crashed (store=%s id=%s): %v\nstderr: %s", store, id, err, stderr.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("python_fixture_helper.py read produced invalid JSON (store=%s id=%s): %v\nstdout: %s", store, id, err, out.String())
	}

	if errMsg, ok := result["error"].(string); ok && errMsg != "" {
		t.Fatalf("python_fixture_helper.py read returned error (store=%s id=%s): %s\nstderr: %s", store, id, errMsg, stderr.String())
	}

	if found, ok := result["found"].(bool); ok && !found {
		return nil
	}

	m := make(bson.M, len(result))
	for k, v := range result {
		m[k] = v
	}
	return m
}

// resolvePythonPath returns a PYTHONPATH string for parity tests.
// It checks $OTOXAN_HOME/scripts and $OTOXAN_HOME/profiles/default/skills,
// falling back to ~/.local/share/otoxan/... if OTOXAN_HOME is not set.
func resolvePythonPath() string {
	home := os.Getenv("OTOXAN_HOME")
	if home == "" {
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			home = filepath.Join(xdg, "otoxan")
		} else {
			h, _ := os.UserHomeDir()
			home = filepath.Join(h, ".local", "share", "otoxan")
		}
	}
	scripts := filepath.Join(home, "scripts")
	skills := filepath.Join(home, "profiles", "default", "skills")
	return scripts + string(filepath.ListSeparator) + skills
}

// MustGetEnv returns the value of an environment variable or fails the test.
func MustGetEnv(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	if v == "" {
		t.Fatalf("required environment variable %s is not set", key)
	}
	return v
}

// NormalizeTimeFields converts ISO-8601 string values in a bson.M to time.Time
// so that Go/Python round-trip comparisons work. Only keys ending in "_at" or
// named "created_at" / "updated_at" / "deleted_at" / "sent_at" / "read_at" /
// "started_at" / "claimed_at" / "completed_at" / "flow_completed_at" / "archived_at"
// are touched.
func NormalizeTimeFields(t *testing.T, m bson.M) {
	t.Helper()
	timeKeys := map[string]bool{
		"created_at": true, "updated_at": true, "deleted_at": true,
		"sent_at": true, "read_at": true, "started_at": true,
		"claimed_at": true, "completed_at": true, "flow_completed_at": true,
		"archived_at": true, "scheduled_for": true,
	}
	for k, v := range m {
		switch val := v.(type) {
		case string:
			if timeKeys[k] || (len(k) > 3 && k[len(k)-3:] == "_at") {
				if parsed, err := time.Parse(time.RFC3339Nano, val); err == nil {
					m[k] = parsed.UTC().Truncate(time.Millisecond)
				} else if parsed, err := time.Parse(time.RFC3339, val); err == nil {
					m[k] = parsed.UTC().Truncate(time.Millisecond)
				}
			}
		case map[string]interface{}:
			// Recurse into nested maps (e.g. retry_config, failure_context)
			nested := bson.M(val)
			NormalizeTimeFields(t, nested)
			m[k] = nested
		case []interface{}:
			// Recurse into arrays of maps (e.g. artifacts, steps)
			for i, item := range val {
				if sub, ok := item.(map[string]interface{}); ok {
					nested := bson.M(sub)
					NormalizeTimeFields(t, nested)
					val[i] = nested
				}
			}
		}
	}
}
