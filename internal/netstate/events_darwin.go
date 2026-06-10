//go:build darwin

package netstate

import (
	"context"
	"syscall"
)

// WatchRouteChanges returns a channel that fires whenever the kernel routing
// table or interface set changes (PF_ROUTE socket). This is the event source
// behind the millisecond-latency Cisco detection (D5). Best-effort: on error
// the channel is closed. Read-only and unprivileged; never touches Cisco.
func WatchRouteChanges(ctx context.Context) <-chan struct{} {
	ch := make(chan struct{}, 1)
	fd, err := syscall.Socket(syscall.AF_ROUTE, syscall.SOCK_RAW, 0)
	if err != nil {
		close(ch)
		return ch
	}
	go func() {
		<-ctx.Done()
		_ = syscall.Close(fd) // unblock the Read below
	}()
	go func() {
		defer close(ch)
		buf := make([]byte, 4096)
		for {
			n, err := syscall.Read(fd, buf)
			if err != nil || n <= 0 {
				return
			}
			select {
			case ch <- struct{}{}:
			default: // coalesce — one pending wake-up is enough
			}
		}
	}()
	return ch
}
