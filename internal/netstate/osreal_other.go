//go:build !darwin

package netstate

import (
	"context"
	"os/exec"
)

type osRunner struct{}

// NewOSRunner keeps the command-backed detector available to non-Darwin
// builds. Darwin production uses NewKernel instead.
func NewOSRunner() CommandRunner { return osRunner{} }

func (osRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// NewKernel is the portable composition-root fallback. Only Darwin has a
// kernel RIB implementation; other targets retain the existing read-only
// command source.
func NewKernel() ObservationSource { return New(NewOSRunner()) }
