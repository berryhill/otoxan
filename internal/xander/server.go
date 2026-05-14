//go:build xander

package xander

import (
	"net"

	"github.com/silas/otoxan/internal/version"
	"golang.org/x/sys/unix"
)

// Version is the Xander package version, kept in lock-step with the binary.
var Version = version.Short()

// realAuthorize is the build-tag-gated implementation of PeerCredAuthorizer.Authorize.
// It uses SO_PEERCRED via golang.org/x/sys/unix and is only compiled for xander builds.
// When called, it replaces the stub in client_types.go for production use.
func (a *PeerCredAuthorizer) realAuthorize(conn *net.UnixConn) error {
	f, err := conn.File()
	if err != nil {
		return err
	}
	defer f.Close()

	cred, err := unix.GetsockoptUcred(int(f.Fd()), unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return err
	}

	if cred.Uid != a.ExpectedUID {
		return &peerCredError{got: cred.Uid, want: a.ExpectedUID}
	}
	return nil
}

// peerCredError carries structured UID mismatch details.
type peerCredError struct {
	got, want uint32
}

func (e *peerCredError) Error() string {
	return "xander: rejected connection from uid " + string(rune(e.got)) + " (expected " + string(rune(e.want)) + ")"
}
