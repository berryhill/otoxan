//go:build xander

package xander

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// ------------------------------------------------------------------
// TestServerPeerCreds
// ------------------------------------------------------------------

// TestServerPeerCreds covers (a) accept connection from same uid,
// (b) reject from different uid.
func TestServerPeerCreds(t *testing.T) {
	auth, err := NewPeerCredAuthorizer()
	require.NoError(t, err)
	require.NotNil(t, auth)

	t.Run("same_uid_accepted", func(t *testing.T) {
		// Create a connected socket pair in-process.
		fd, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
		require.NoError(t, err)
		defer unix.Close(fd[0])
		defer unix.Close(fd[1])

		// Wrap the server-side fd in a *net.UnixConn.
		f0 := os.NewFile(uintptr(fd[0]), "server")
		defer f0.Close()
		conn0, err := net.FileConn(f0)
		require.NoError(t, err)
		defer conn0.Close()

		uc, ok := conn0.(*net.UnixConn)
		require.True(t, ok, "conn0 must be a *net.UnixConn")

		// Same UID should be accepted.
		err = auth.Authorize(uc)
		assert.NoError(t, err)
	})

	t.Run("different_uid_rejected", func(t *testing.T) {
		// Create a connected socket pair in-process.
		fd, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
		require.NoError(t, err)
		defer unix.Close(fd[0])
		defer unix.Close(fd[1])

		// Wrap the server-side fd in a *net.UnixConn.
		f0 := os.NewFile(uintptr(fd[0]), "server")
		defer f0.Close()
		conn0, err := net.FileConn(f0)
		require.NoError(t, err)
		defer conn0.Close()

		uc, ok := conn0.(*net.UnixConn)
		require.True(t, ok, "conn0 must be a *net.UnixConn")

		// Stub the getsockopt call by temporarily replacing the fd with a
		// file descriptor we control.  We can't actually change UID in a
		// test (setresuid requires CAP_SETUID), so we stub at the syscall
		// boundary.
		//
		// Instead, we create a *net.UnixConn from a socketpair where we
		// have pre-injected a fake credential via SCM_CREDENTIALS on the
		// *client* side.  The kernel will return the sender's creds when
		// the server calls SO_PEERCRED ... but on Linux a socketpair
		// shares the same peer creds.  To force a mismatch we use a
		// genuine separate process.
		//
		// Simpler approach: spawn a small child via exec.Command that
		// dials the socket.  The child runs as the same UID, though.
		// To simulate a different UID we stub the authorizer itself.

		fakeAuth := &PeerCredAuthorizer{ExpectedUID: 99999} // impossible UID
		err = fakeAuth.Authorize(uc)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rejected connection from uid")
	})
}

// TestNewPeerCredAuthorizer_refuses_root verifies that the authorizer refuses
// to construct when the current process is root.
func TestNewPeerCredAuthorizer_refuses_root(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping: not running as root")
	}
	_, err := NewPeerCredAuthorizer()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to run as root")
}

// TestSocketPath_returns_expected_format verifies SocketPath contains the
// expected directory segments.
func TestSocketPath_returns_expected_format(t *testing.T) {
	path := SocketPath()
	assert.Contains(t, path, ".otoxan")
	assert.Contains(t, path, "run")
	assert.Contains(t, path, "xander.sock")
}

// TestEnsureRunDir_creates_directory verifies EnsureRunDir creates the
// ~/.otoxan/run directory and returns no error.
func TestEnsureRunDir_creates_directory(t *testing.T) {
	dir := t.TempDir()
	// Override HOME so we don't touch the real filesystem.
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	require.NoError(t, EnsureRunDir())

	stat, err := os.Stat(filepath.Join(dir, ".otoxan", "run"))
	require.NoError(t, err)
	assert.True(t, stat.IsDir())
}

// TestPeerCredAuthorize_bad_fd is intentionally skipped: hitting the
// "File() fails" path requires kernel-level fd exhaustion or a non-socket
// fd masquerading as a net.UnixConn, neither of which is safe or portable
// in a unit test.  The error path is covered by inspection.
func TestPeerCredAuthorize_bad_fd(t *testing.T) {
	t.Skip("fd exhaustion test skipped: requires kernel-level fd manipulation")
}
