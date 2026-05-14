package runtime

import (
	"fmt"
	"os"
	"path/filepath"
)

// Mode describes where the otoxan CLI process is running relative to the
// runtime services (MongoDB, Qdrant, state.db, etc.).
type Mode string

const (
	// ModeLocal means both the CLI and the runtime run on the same machine
	// (the workstation).  There is no network boundary between the binary
	// and the backing stores.  This is the only first-class mode in v1.
	ModeLocal Mode = "local"

	// ModeSSH means the CLI binary runs on the user's laptop but the runtime
	// still lives on the workstation, reached over an SSH tunnel.  The user
	// experience is identical to local, but the network boundary means
	// state.db must be accessed through an SSH-forwarded path or a remote
	// proxy, and credential resolution may differ.
	ModeSSH Mode = "ssh"

	// ModeGateway means the CLI (or a phone client) talks to a remote
	// workstation through a gateway endpoint (HTTPS / gRPC / similar).
	// The runtime is fully remote; sessions are attached via the gateway
	// API rather than by opening local files.
	ModeGateway Mode = "gateway"
)

// DetectMode returns the current runtime mode based on environment heuristics.
//
// Priority:
//  1. OTOXAN_MODE env var (explicit override)
//  2. If $HOME/.otoxan is missing and SSH_CONNECTION is set → ModeSSH
//  3. If $HOME/.otoxan exists and we are on the workstation → ModeLocal
//  4. Fallback → ModeLocal (v1 safe default)
//
// The heuristic is intentionally conservative.  In v1 the user must set
// OTOXAN_MODE=gateway to trigger gateway behaviour; auto-detection for
// gateway is deferred until the gateway binary exists and advertises itself.
func DetectMode() Mode {
	// 1. Explicit override.
	if m := os.Getenv("OTOXAN_MODE"); m != "" {
		switch m {
		case string(ModeLocal), string(ModeSSH), string(ModeGateway):
			return Mode(m)
		default:
			// Unknown value: fall through to heuristic rather than panic.
		}
	}

	// 2. Gateway heuristic: if a gateway URL is configured, the runtime is
	//    remote and we talk to it through the gateway endpoint.
	if os.Getenv("OTOXAN_GATEWAY_URL") != "" {
		return ModeGateway
	}

	// 3. SSH heuristic: if we are in an SSH session and there is no local
	//    otoxan home, the runtime is almost certainly remote.
	if os.Getenv("SSH_CONNECTION") != "" {
		home, err := os.UserHomeDir()
		if err == nil {
			otoxanHome := filepath.Join(home, ".otoxan")
			if _, err := os.Stat(otoxanHome); os.IsNotExist(err) {
				return ModeSSH
			}
		}
	}

	// 4. Default to local for v1.  The workstation is the only supported
	//    first-class environment in this version.
	return ModeLocal
}

// Validate returns an error if the mode is not one of the known constants.
func (m Mode) Validate() error {
	switch m {
	case ModeLocal, ModeSSH, ModeGateway:
		return nil
	default:
		return fmt.Errorf("runtime: unknown mode %q", m)
	}
}

// IsRemote returns true when the runtime services are not on the same host
// as the CLI process.  In remote modes session attachment crosses a network
// boundary, so state.db and credential handling must use remote-aware paths.
func (m Mode) IsRemote() bool {
	switch m {
	case ModeSSH, ModeGateway:
		return true
	default:
		return false
	}
}
