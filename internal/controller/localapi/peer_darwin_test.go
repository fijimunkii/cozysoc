//go:build darwin

package localapi

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestPeerIdentityFromDarwinCred(t *testing.T) {
	identity, err := peerIdentityFromDarwinCred(&unix.Xucred{
		Version: darwinXucredVersion,
		Uid:     501,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !identity.Verified || identity.UID != 501 {
		t.Fatalf("unexpected identity: %+v", identity)
	}
}

func TestPeerIdentityFromDarwinCredRejectsUnexpectedVersion(t *testing.T) {
	if _, err := peerIdentityFromDarwinCred(&unix.Xucred{Version: darwinXucredVersion + 1}); err == nil {
		t.Fatal("unexpected xucred version was accepted")
	}
}
