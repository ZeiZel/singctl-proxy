//go:build darwin

package proclist

import (
	"context"
	"os/exec"
)

// NewLister returns the macOS process lister backed by lsof.
func NewLister() Lister { return lsofLister{} }

type lsofLister struct{}

func (lsofLister) List(ctx context.Context) ([]Process, error) {
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
	return parseLsofFields(out), nil
}
