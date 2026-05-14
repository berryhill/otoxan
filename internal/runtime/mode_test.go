package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectMode_Default(t *testing.T) {
	// Clear all relevant env vars so the default heuristic runs.
	t.Setenv("OTOXAN_MODE", "")
	t.Setenv("OTOXAN_GATEWAY_URL", "")
	t.Setenv("SSH_CONNECTION", "")

	// Ensure $HOME/.otoxan exists so the SSH heuristic does not trigger.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}
	otoxanHome := filepath.Join(home, ".otoxan")
	if err := os.MkdirAll(otoxanHome, 0755); err != nil {
		t.Skipf("cannot create %s: %v", otoxanHome, err)
	}

	mode := DetectMode()
	if mode != ModeLocal {
		t.Errorf("DetectMode() = %q, want %q", mode, ModeLocal)
	}
}

func TestDetectMode_GatewayURL(t *testing.T) {
	// When OTOXAN_GATEWAY_URL is set, the heuristic should detect ModeGateway.
	t.Setenv("OTOXAN_MODE", "")
	t.Setenv("OTOXAN_GATEWAY_URL", "https://gw.example")
	t.Setenv("SSH_CONNECTION", "")

	mode := DetectMode()
	if mode != ModeGateway {
		t.Errorf("DetectMode() = %q, want %q", mode, ModeGateway)
	}
}

func TestDetectMode_SSH(t *testing.T) {
	// When SSH_CONNECTION is set and $HOME/.otoxan does not exist,
	// the heuristic should detect ModeSSH.
	t.Setenv("OTOXAN_MODE", "")
	t.Setenv("OTOXAN_GATEWAY_URL", "")
	t.Setenv("SSH_CONNECTION", "192.168.1.1 12345 192.168.1.2 22")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}
	otoxanHome := filepath.Join(home, ".otoxan")
	// Rename .otoxan out of the way if it exists.
	if _, err := os.Stat(otoxanHome); err == nil {
		backup := otoxanHome + ".bak"
		if err := os.Rename(otoxanHome, backup); err != nil {
			t.Skipf("cannot rename %s: %v", otoxanHome, err)
		}
		defer os.Rename(backup, otoxanHome)
	}

	mode := DetectMode()
	if mode != ModeSSH {
		t.Errorf("DetectMode() = %q, want %q", mode, ModeSSH)
	}
}

func TestDetectMode_ExplicitOverride(t *testing.T) {
	// OTOXAN_MODE should override everything else.
	t.Setenv("OTOXAN_MODE", "gateway")
	t.Setenv("OTOXAN_GATEWAY_URL", "")
	t.Setenv("SSH_CONNECTION", "")

	mode := DetectMode()
	if mode != ModeGateway {
		t.Errorf("DetectMode() = %q, want %q", mode, ModeGateway)
	}
}

func TestDetectMode_GarbageOverride(t *testing.T) {
	// An unknown OTOXAN_MODE value should fall through to default heuristics.
	t.Setenv("OTOXAN_MODE", "garbage")
	t.Setenv("OTOXAN_GATEWAY_URL", "")
	t.Setenv("SSH_CONNECTION", "")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}
	otoxanHome := filepath.Join(home, ".otoxan")
	if err := os.MkdirAll(otoxanHome, 0755); err != nil {
		t.Skipf("cannot create %s: %v", otoxanHome, err)
	}

	mode := DetectMode()
	if mode != ModeLocal {
		t.Errorf("DetectMode() = %q, want %q (garbage override should fall through)", mode, ModeLocal)
	}
}
