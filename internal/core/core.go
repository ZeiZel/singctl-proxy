// Package core isolates the embedded sing-box behind a small interface. It is
// the ONLY package allowed to import github.com/sagernet/sing-box, and that
// import lives exclusively in real.go behind the `singbox` build tag — so the
// default build and the unit suite never pull the (heavy, CGO) library and run
// entirely on FakeCore.
package core

import "context"

// Core is the lifecycle of a single sing-box instance.
type Core interface {
	// Start brings the instance up (opens inbounds, dials nothing yet).
	Start(ctx context.Context) error
	// Close tears it down and (for the real core) restores routes it added.
	Close() error
}

// Factory builds a Core from a sing-box JSON config. label identifies the
// instance ("proxy" / "forwarder") for logging.
type Factory func(ctx context.Context, label string, configJSON []byte) (Core, error)
