//go:build darwin

package proclist

import (
	"context"
	"os/exec"
)

// NewLister returns the macOS process lister backed by lsof (sockets) + ps
// (parent chain, for grouping helpers under their main app).
func NewLister() Lister { return lsofLister{} }

type lsofLister struct{}

func (lsofLister) List(ctx context.Context) ([]App, error) {
	// Field output (-F) is stable and easy to parse: p=PID, c=command, n=name.
	// -nP avoids slow DNS/port-name lookups; -iTCP limits to TCP sockets.
	out, err := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-FpcPn", "-iTCP").Output()
	if err != nil {
		// lsof exits non-zero when some sockets are unreadable but still prints
		// usable output; parse whatever we got.
		if len(out) == 0 {
			return nil, err
		}
	}
	procs := parseLsofFields(out)

	// ps gives the parent chain + names for ALL processes (lsof has no ppid), so
	// helpers can be folded under their main app. -ww disables column truncation.
	ppid, name := map[int]int{}, map[int]string{}
	if psOut, perr := exec.CommandContext(ctx, "/bin/ps", "-axww", "-o", "pid=,ppid=,comm=").Output(); perr == nil {
		ppid, name = parsePSPpid(psOut)
	}
	return groupApps(procs, ppid, name), nil
}
