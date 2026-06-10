//go:build !singbox

package core

import (
	"context"
	"errors"
	"testing"
)

func TestStubFactory_ErrsWithoutSingbox(t *testing.T) {
	// In the default (no `singbox` tag) build, NewFactory must not link sing-box
	// and must return ErrNoSingbox so the failure is explicit.
	_, err := NewFactory()(context.Background(), "proxy", nil)
	if !errors.Is(err, ErrNoSingbox) {
		t.Fatalf("want ErrNoSingbox, got %v", err)
	}
}
