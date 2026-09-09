//go:build darwin

package secretstore

import (
	"errors"
	"testing"

	"github.com/lexfrei/keychain"
)

func TestMapKeychainError(t *testing.T) {
	cases := []struct {
		upstream error
		want     error
	}{
		{keychain.ErrNotFound, ErrNotFound},
		{keychain.ErrLocked, ErrLocked},
		{keychain.ErrUnavailable, ErrUnavailable},
		{keychain.ErrAccessDenied, ErrAccessDenied},
		{keychain.ErrUnsupported, ErrUnsupported},
		{keychain.ErrInvalidKey, ErrInvalidRef},
	}
	ref, err := ParseReference("integration/test")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		if got := mapKeychainError("get", ref, tc.upstream); !errors.Is(got, tc.want) {
			t.Fatalf("mapKeychainError(%v) = %v, want %v", tc.upstream, got, tc.want)
		}
	}
}
