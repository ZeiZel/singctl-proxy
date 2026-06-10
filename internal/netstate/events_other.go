//go:build !darwin

package netstate

import "context"

// WatchRouteChanges has no kernel event source on this platform yet; it
// returns nil, which blocks forever in the monitor's select, so detection
// relies on the 2s polling fallback alone. (A closed channel would busy-loop
// the monitor — nil is the documented "no events" value for Monitor.Run.)
func WatchRouteChanges(ctx context.Context) <-chan struct{} {
	return nil
}
