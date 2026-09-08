package localapi

import "net"

type PeerIdentity struct {
	UID      int
	PID      int
	Verified bool
}

type peerVerifier func(net.Conn) (PeerIdentity, error)
