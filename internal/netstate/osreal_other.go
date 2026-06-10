//go:build !darwin

package netstate

import (
	"context"
	"os/exec"
)

// osRunner is the portable, read-only CommandRunner for non-darwin builds. It
// resolves binaries from PATH; commands that don't exist on the platform
// (e.g. macOS-style ifconfig/netstat output) simply fail and the detector
// skips that tick — passive Cisco detection degrades gracefully.
type osRunner struct{}

// NewOSRunner returns the production CommandRunner.
func NewOSRunner() CommandRunner { return osRunner{} }

func (osRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}
