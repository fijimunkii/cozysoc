//go:build !linux

package localapi

import "net"

func verifyPeer(net.Conn) (PeerIdentity, error) {
	return PeerIdentity{Verified: false}, nil
}
