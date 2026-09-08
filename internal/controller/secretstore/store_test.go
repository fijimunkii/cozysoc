package secretstore

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"
)

func TestParseReference(t *testing.T) {
	valid := []string{"adguard/default", "router.opnsense-1", "credential_01"}
	for _, value := range valid {
		if _, err := ParseReference(value); err != nil {
			t.Fatalf("valid reference %q rejected: %v", value, err)
		}
	}

	invalid := []string{"", "/leading", "Uppercase", "has space", "line\nbreak"}
	for _, value := range invalid {
		if _, err := ParseReference(value); !errors.Is(err, ErrInvalidRef) {
			t.Fatalf("invalid reference %q error = %v, want ErrInvalidRef", value, err)
		}
	}
}

func TestSecretCopiesAndRedacts(t *testing.T) {
	input := []byte("household-secret")
	secret := NewSecret(input)
	input[0] = 'X'

	got := secret.Bytes()
	if string(got) != "household-secret" {
		t.Fatalf("secret did not copy input: %q", got)
	}
	got[0] = 'Y'
	if string(secret.Bytes()) != "household-secret" {
		t.Fatal("secret Bytes returned mutable backing storage")
	}
	if fmt.Sprint(secret) != "[redacted]" {
		t.Fatalf("String leaked secret: %s", secret)
	}
	if value := secret.LogValue(); value.Kind() != slog.KindString || value.String() != "[redacted]" {
		t.Fatalf("LogValue did not redact: %v", value)
	}
}

func TestHeadlessStoreFailsClosedWithoutProvisioning(t *testing.T) {
	store, err := NewHeadless()
	if store != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("headless store = %v, err = %v; want unavailable", store, err)
	}
}
