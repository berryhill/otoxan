package firstrun

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsFirstRun_missingSentinel(t *testing.T) {
	dir := t.TempDir()
	got, err := IsFirstRun(dir)
	if err != nil {
		t.Fatalf("IsFirstRun error: %v", err)
	}
	if !got {
		t.Fatalf("expected IsFirstRun=true for missing sentinel, got false")
	}
}

func TestMarkOnboardingComplete_roundTrip(t *testing.T) {
	dir := t.TempDir()

	if err := MarkOnboardingComplete(dir); err != nil {
		t.Fatalf("MarkOnboardingComplete error: %v", err)
	}

	got, err := IsFirstRun(dir)
	if err != nil {
		t.Fatalf("IsFirstRun error: %v", err)
	}
	if got {
		t.Fatalf("expected IsFirstRun=false after marking complete, got true")
	}

	// Verify the sentinel file exists with expected content.
	path := filepath.Join(dir, SentinelFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(data) != "1\n" {
		t.Fatalf("unexpected sentinel content %q, want \"1\\n\"", string(data))
	}
}

func TestIsFirstRun_emptyHome(t *testing.T) {
	_, err := IsFirstRun("")
	if err == nil {
		t.Fatal("expected error for empty home, got nil")
	}
}

func TestMarkOnboardingComplete_emptyHome(t *testing.T) {
	err := MarkOnboardingComplete("")
	if err == nil {
		t.Fatal("expected error for empty home, got nil")
	}
}

func TestMarkOnboardingComplete_createsHome(t *testing.T) {
	parent := t.TempDir()
	home := filepath.Join(parent, "nested", "otoxan")

	if err := MarkOnboardingComplete(home); err != nil {
		t.Fatalf("MarkOnboardingComplete error: %v", err)
	}

	info, err := os.Stat(home)
	if err != nil {
		t.Fatalf("expected home dir to be created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected home to be a directory")
	}
}
