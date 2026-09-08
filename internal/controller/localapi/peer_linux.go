//go:build linux

package localapi

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

func verifyPeer(conn net.Conn) (PeerIdentity, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return PeerIdentity{}, fmt.Errorf("unexpected local API connection type %T", conn)
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return PeerIdentity{}, fmt.Errorf("access local API socket descriptor: %w", err)
	}

	var cred *unix.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		cred, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return PeerIdentity{}, fmt.Errorf("inspect local API peer: %w", err)
	}
	if socketErr != nil {
		return PeerIdentity{}, fmt.Errorf("read local API peer credentials: %w", socketErr)
	}
	if cred == nil {
		return PeerIdentity{}, fmt.Errorf("local API peer credentials unavailable")
	}
	return PeerIdentity{UID: int(cred.Uid), PID: int(cred.Pid), Verified: true}, nil
}
