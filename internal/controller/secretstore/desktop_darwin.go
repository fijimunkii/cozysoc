//go:build darwin

package secretstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/lexfrei/keychain"
)

const macOSKeychainService = "com.cozysoc.credentials.v1"

type macOSKeychainStore struct {
	keychain *keychain.Keychain
}

func NewDesktop() (Store, error) {
	return &macOSKeychainStore{
		keychain: keychain.New(keychain.WithAccessMode(keychain.TrustCurrentApp)),
	}, nil
}

func (s *macOSKeychainStore) Put(ctx context.Context, ref Reference, secret Secret) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.keychain.Set(macOSKeychainService, ref.String(), secret.Bytes()); err != nil {
		return mapKeychainError("put", ref, err)
	}
	return ctx.Err()
}

func (s *macOSKeychainStore) Get(ctx context.Context, ref Reference) (Secret, error) {
	if err := ctx.Err(); err != nil {
		return Secret{}, err
	}
	value, err := s.keychain.Get(macOSKeychainService, ref.String())
	if err != nil {
		return Secret{}, mapKeychainError("get", ref, err)
	}
	if err := ctx.Err(); err != nil {
		return Secret{}, err
	}
	return NewSecret(value), nil
}

func (s *macOSKeychainStore) Delete(ctx context.Context, ref Reference) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.keychain.Delete(macOSKeychainService, ref.String()); err != nil {
		return mapKeychainError("delete", ref, err)
	}
	return ctx.Err()
}

func (*macOSKeychainStore) Backend() BackendInfo {
	return BackendInfo{
		Kind:                       "macos-keychain",
		Persistent:                 true,
		OSBacked:                   true,
		InteractiveSessionRequired: true,
	}
}

func mapKeychainError(operation string, ref Reference, err error) error {
	mapped := err
	switch {
	case errors.Is(err, keychain.ErrNotFound):
		mapped = ErrNotFound
	case errors.Is(err, keychain.ErrLocked):
		mapped = ErrLocked
	case errors.Is(err, keychain.ErrUnavailable):
		mapped = ErrUnavailable
	case errors.Is(err, keychain.ErrAccessDenied):
		mapped = ErrAccessDenied
	case errors.Is(err, keychain.ErrUnsupported):
		mapped = ErrUnsupported
	case errors.Is(err, keychain.ErrInvalidKey):
		mapped = ErrInvalidRef
	}
	return fmt.Errorf("macOS Keychain %s %q: %w", operation, ref.String(), mapped)
}
