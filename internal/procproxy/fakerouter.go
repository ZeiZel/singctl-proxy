package procproxy

import (
	"context"
	"sort"
)

// FakeRouter is an in-memory Router for tests (mirrors core.FakeCore). It records
// calls and never touches the kernel or spawns processes.
type FakeRouter struct {
	Added     []int
	Removed   []int
	Unrouted  []int
	Killed    []int
	Restarted []int
	Launched  [][]string
	CleanedUp bool
	NextPID   int
	AddErr    error
	LaunchErr error
	Routed    []int

	// BundlesByPID optionally maps a routed PID to a bundle ID, so a test can
	// make FakeRouter satisfy BundleRouter (for whole-app routing tests) without
	// a real darwin extension. Populated by the test.
	BundlesByPID map[int]string
}

func (f *FakeRouter) AddPID(_ context.Context, pid int) error {
	if f.AddErr != nil {
		return f.AddErr
	}
	f.Added = append(f.Added, pid)
	f.Routed = append(f.Routed, pid)
	return nil
}

func (f *FakeRouter) RemovePID(_ context.Context, pid int) error {
	f.Removed = append(f.Removed, pid)
	for i, p := range f.Routed {
		if p == pid {
			f.Routed = append(f.Routed[:i], f.Routed[i+1:]...)
			break
		}
	}
	return nil
}

func (f *FakeRouter) Launch(_ context.Context, argv []string) (int, error) {
	if f.LaunchErr != nil {
		return 0, f.LaunchErr
	}
	f.Launched = append(f.Launched, argv)
	pid := f.NextPID
	if pid == 0 {
		pid = 1000 + len(f.Launched)
	}
	f.Routed = append(f.Routed, pid)
	return pid, nil
}

func (f *FakeRouter) RestartPID(_ context.Context, pid int) (int, error) {
	if f.LaunchErr != nil {
		return 0, f.LaunchErr
	}
	f.Restarted = append(f.Restarted, pid)
	newPID := f.NextPID
	if newPID == 0 {
		newPID = 9000 + len(f.Restarted)
	}
	f.Routed = append(f.Routed, newPID)
	return newPID, nil
}

func (f *FakeRouter) Unroute(ctx context.Context, pid int) error {
	f.Unrouted = append(f.Unrouted, pid)
	return f.RemovePID(ctx, pid)
}

func (f *FakeRouter) Kill(_ context.Context, pid int) error {
	f.Killed = append(f.Killed, pid)
	for i, p := range f.Routed {
		if p == pid {
			f.Routed = append(f.Routed[:i], f.Routed[i+1:]...)
			break
		}
	}
	return nil
}

func (f *FakeRouter) ListRouted() []int { return f.Routed }

func (f *FakeRouter) Cleanup() error {
	f.CleanedUp = true
	return nil
}

// PIDsForBundle implements BundleRouter, mirroring darwinRouter's byPID lookup
// over the test-populated BundlesByPID map.
func (f *FakeRouter) PIDsForBundle(bundleID string) []int {
	var out []int
	for pid, id := range f.BundlesByPID {
		if id == bundleID {
			out = append(out, pid)
		}
	}
	sort.Ints(out)
	return out
}

// RoutedBundleIDs implements BundleRouter, mirroring darwinRouter's refs keys.
func (f *FakeRouter) RoutedBundleIDs() []string {
	seen := map[string]bool{}
	for _, id := range f.BundlesByPID {
		seen[id] = true
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
