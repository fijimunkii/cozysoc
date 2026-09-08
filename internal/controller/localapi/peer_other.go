//go:build !linux && !darwin

package localapi

import "net"

func verifyPeer(net.Conn) (PeerIdentity, error) {
	return PeerIdentity{Verified: false}, nil
}
