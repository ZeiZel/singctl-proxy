//go:build darwin

package netstate

import (
	"context"
	"os/exec"
)

// osRunner is the real, read-only CommandRunner for macOS. It maps logical names
// to absolute paths (never a Cisco binary) and shells out.
type osRunner struct{}

// NewOSRunner returns the production CommandRunner.
func NewOSRunner() CommandRunner { return osRunner{} }

var absPath = map[string]string{
	"ifconfig": "/sbin/ifconfig",
	"netstat":  "/usr/sbin/netstat",
	"route":    "/sbin/route",
	"ps":       "/bin/ps",
}

func (osRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	bin := name
	if p, ok := absPath[name]; ok {
		bin = p
	}
	return exec.CommandContext(ctx, bin, args...).Output()
}
