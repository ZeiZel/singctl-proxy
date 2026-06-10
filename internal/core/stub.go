//go:build !singbox

package core

import (
	"context"
	"errors"
)

// ErrNoSingbox is returned by the stub factory in builds without the `singbox`
// tag (the hermetic unit/dev build). The shipping binary is built with
// `-tags singbox`; see Makefile and README-build.md.
var ErrNoSingbox = errors.New("core: built without sing-box; rebuild with -tags singbox")

// NewFactory returns a Factory that always errors, so the default binary and
// the unit suite link without the sing-box library. Tests use FakeCore directly.
func NewFactory() Factory {
	return func(ctx context.Context, label string, configJSON []byte) (Core, error) {
		return nil, ErrNoSingbox
	}
}
