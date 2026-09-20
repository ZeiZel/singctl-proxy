//go:build !windows

package daemon

import "testing"

// TestStripMallocStackLoggingEnv is F2 item 6's regression test for the
// daemon's own re-exec (Spawn): MallocStackLogging* must not reach the child,
// the same way internal/procproxy strips it from launched-app children.
func TestStripMallocStackLoggingEnv(t *testing.T) {
	in := []string{
		"HOME=/root",
		"MallocStackLogging=1",
		"MallocStackLoggingNoCompact=1",
		"PATH=/usr/bin",
	}
	got := stripMallocStackLoggingEnv(in)
	want := []string{"HOME=/root", "PATH=/usr/bin"}
	if len(got) != len(want) {
		t.Fatalf("stripMallocStackLoggingEnv(%v) = %v, want %v", in, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
