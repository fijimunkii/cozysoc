//go:build darwin

package localapi

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

const darwinXucredVersion = 0

func verifyPeer(conn net.Conn) (PeerIdentity, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return PeerIdentity{}, fmt.Errorf("unexpected local API connection type %T", conn)
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return PeerIdentity{}, fmt.Errorf("access local API socket descriptor: %w", err)
	}

	var cred *unix.Xucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		cred, socketErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return PeerIdentity{}, fmt.Errorf("inspect local API peer: %w", err)
	}
	if socketErr != nil {
		return PeerIdentity{}, fmt.Errorf("read local API peer credentials: %w", socketErr)
	}
	return peerIdentityFromDarwinCred(cred)
}

func peerIdentityFromDarwinCred(cred *unix.Xucred) (PeerIdentity, error) {
	if cred == nil {
		return PeerIdentity{}, fmt.Errorf("local API peer credentials unavailable")
	}
	if cred.Version != darwinXucredVersion {
		return PeerIdentity{}, fmt.Errorf("unsupported Darwin peer credential version %d", cred.Version)
	}
	return PeerIdentity{UID: int(cred.Uid), Verified: true}, nil
}
