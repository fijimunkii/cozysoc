package secretstore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

const maxReferenceLength = 128

var (
	ErrNotFound     = errors.New("secret not found")
	ErrUnavailable  = errors.New("secret store unavailable")
	ErrLocked       = errors.New("secret store locked")
	ErrAccessDenied = errors.New("secret store access denied")
	ErrUnsupported  = errors.New("secret store unsupported")
	ErrInvalidRef   = errors.New("invalid secret reference")
)

type Reference string

func ParseReference(value string) (Reference, error) {
	if len(value) == 0 || len(value) > maxReferenceLength {
		return "", fmt.Errorf("%w: length must be 1..%d", ErrInvalidRef, maxReferenceLength)
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		allowed := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '/'
		if !allowed {
			return "", fmt.Errorf("%w: references use lowercase ASCII letters, digits, '.', '_', '-', and '/'", ErrInvalidRef)
		}
	}
	first := value[0]
	if !((first >= 'a' && first <= 'z') || (first >= '0' && first <= '9')) {
		return "", fmt.Errorf("%w: reference must start with a letter or digit", ErrInvalidRef)
	}
	return Reference(value), nil
}

type Secret struct {
	value []byte
}

func NewSecret(value []byte) Secret {
	return Secret{value: append([]byte(nil), value...)}
}

func (s Secret) Bytes() []byte {
	return append([]byte(nil), s.value...)
}

func (s Secret) Len() int {
	return len(s.value)
}

func (Secret) String() string {
	return "[redacted]"
}

func (Secret) LogValue() slog.Value {
	return slog.StringValue("[redacted]")
}

type BackendInfo struct {
	Kind                       string `json:"kind"`
	Persistent                 bool   `json:"persistent"`
	OSBacked                   bool   `json:"os_backed"`
	InteractiveSessionRequired bool   `json:"interactive_session_required"`
}

type Store interface {
	Put(context.Context, Reference, Secret) error
	Get(context.Context, Reference) (Secret, error)
	Delete(context.Context, Reference) error
	Backend() BackendInfo
}
