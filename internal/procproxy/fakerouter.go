package procproxy

import "context"

// FakeRouter is an in-memory Router for tests (mirrors core.FakeCore). It records
// calls and never touches the kernel or spawns processes.
type FakeRouter struct {
	Added     []int
	Removed   []int
	Launched  [][]string
	CleanedUp bool
	NextPID   int
	AddErr    error
	LaunchErr error
	Routed    []int
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

func (f *FakeRouter) ListRouted() []int { return f.Routed }

func (f *FakeRouter) Cleanup() error {
	f.CleanedUp = true
	return nil
}
