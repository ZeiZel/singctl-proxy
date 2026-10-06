//go:build darwin

package netext

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func isolateController(t *testing.T, c Controller) {
	t.Helper()
	c.(*darwinController).cfgPath = filepath.Join(t.TempDir(), "config.json")
}
func TestAvailabilitySingleflightAndInvalidate(t *testing.T) {
	old := extensionProbe
	defer func() { extensionProbe = old; availAt = time.Time{} }()
	availAt = time.Time{}
	availFlight = nil
	availCache = false
	started, release := make(chan struct{}), make(chan struct{})
	calls := 0
	extensionProbe = func() bool {
		calls++
		if calls == 1 {
			close(started)
			<-release
		}
		return true
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); Available() }()
	<-started
	if Cached() {
		t.Fatal("unexpected cached value before probe finishes")
	}
	go func() { defer wg.Done(); Available() }()
	Invalidate() // a user action during the probe must not be lost when it completes
	close(release)
	wg.Wait()
	// The waiter can either join the first flight or refresh after invalidation.
	if calls < 1 || calls > 2 {
		t.Fatalf("probe calls=%d", calls)
	}
	Available()
	if calls != 2 {
		t.Fatalf("lost in-flight invalidation, calls=%d", calls)
	}
	Available()
	if calls != 2 {
		t.Fatalf("cache launched probe, calls=%d", calls)
	}
	Invalidate()
	Available()
	if calls != 3 {
		t.Fatalf("explicit invalidation calls=%d", calls)
	}
}
