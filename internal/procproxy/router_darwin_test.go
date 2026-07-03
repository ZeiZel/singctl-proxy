//go:build darwin

package procproxy

import (
	"context"
	"testing"

	"singctl/internal/netext"
)

// When the extension isn't approved, per-app routing fails with a clear error
// rather than silently no-op'ing.
func TestDarwinRouter_Unavailable(t *testing.T) {
	r := newDarwinRouter(Config{}.withDefaults(), netext.NewFake(false))
	if err := r.AddPID(context.Background(), 123); err != errExtensionUnavailable {
		t.Errorf("AddPID unavailable: got %v, want errExtensionUnavailable", err)
	}
	if _, err := r.Launch(context.Background(), []string{"Cursor"}); err != errExtensionUnavailable {
		t.Errorf("Launch unavailable: got %v, want errExtensionUnavailable", err)
	}
}

// register/deregister refcount a bundle ID across multiple PIDs (Electron
// helpers), so RoutedBundleIDs still reports the app until the last PID goes.
// The router itself no longer touches the controller (the Executor's
// RecomputeAppTargets is the sole writer — see router_darwin.go's doc
// comment), so this only asserts the PID/bundle bookkeeping.
func TestDarwinRouter_Refcount(t *testing.T) {
	fake := netext.NewFake(true)
	r := newDarwinRouter(Config{}.withDefaults(), fake)

	r.register(100, "com.app")
	r.register(101, "com.app") // second helper, same app
	if got := r.RoutedBundleIDs(); len(got) != 1 || got[0] != "com.app" {
		t.Errorf("RoutedBundleIDs = %v, want [com.app]", got)
	}
	if got := r.ListRouted(); len(got) != 2 || got[0] != 100 || got[1] != 101 {
		t.Errorf("ListRouted = %v, want [100 101]", got)
	}
	if len(fake.Adds) != 0 || len(fake.Removes) != 0 {
		t.Errorf("router must not call AddTarget/RemoveTarget directly: adds=%v removes=%v", fake.Adds, fake.Removes)
	}

	r.deregister(100)
	if got := r.RoutedBundleIDs(); len(got) != 1 || got[0] != "com.app" {
		t.Errorf("must still report the app while a PID remains: %v", got)
	}
	r.deregister(101)
	if got := r.RoutedBundleIDs(); len(got) != 0 {
		t.Errorf("app should drop off once its last PID is gone: %v", got)
	}
}

// Cleanup clears all PID/bundle bookkeeping (it does not touch the
// controller — see router_darwin.go's doc comment).
func TestDarwinRouter_Cleanup(t *testing.T) {
	fake := netext.NewFake(true)
	r := newDarwinRouter(Config{}.withDefaults(), fake)
	r.register(1, "com.a")
	r.register(2, "com.b")
	if err := r.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(r.ListRouted()) != 0 || len(r.RoutedBundleIDs()) != 0 {
		t.Errorf("Cleanup should clear all bookkeeping: routed=%v bundles=%v", r.ListRouted(), r.RoutedBundleIDs())
	}
	if len(fake.Removes) != 0 {
		t.Errorf("Cleanup must not call RemoveTarget directly: %v", fake.Removes)
	}
}
