//go:build !darwin

package checkroute

import (
	"context"
	"errors"
	"testing"
)

func TestUnsupportedPlatform(t *testing.T) {
	e, c, _, _ := fixture()
	if _, err := NewInspector().Inspect(context.Background(), e, c); !errors.Is(err, ErrUnsupported) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewInspector().Inspect(ctx, e, c); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
