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
// helpers), so the target is added once and removed only when the last PID goes.
func TestDarwinRouter_Refcount(t *testing.T) {
	fake := netext.NewFake(true)
	r := newDarwinRouter(Config{}.withDefaults(), fake)

	if err := r.register(100, "com.app"); err != nil {
		t.Fatalf("register 100: %v", err)
	}
	if err := r.register(101, "com.app"); err != nil { // second helper, same app
		t.Fatalf("register 101: %v", err)
	}
	if len(fake.Adds) != 1 || fake.Adds[0] != "com.app" {
		t.Errorf("target should be added once: %v", fake.Adds)
	}
	if got := r.ListRouted(); len(got) != 2 || got[0] != 100 || got[1] != 101 {
		t.Errorf("ListRouted = %v, want [100 101]", got)
	}

	if err := r.deregister(100); err != nil {
		t.Fatalf("deregister 100: %v", err)
	}
	if len(fake.Removes) != 0 {
		t.Errorf("must not remove while a PID remains: %v", fake.Removes)
	}
	if err := r.deregister(101); err != nil {
		t.Fatalf("deregister 101: %v", err)
	}
	if len(fake.Removes) != 1 || fake.Removes[0] != "com.app" {
		t.Errorf("target should be removed on last PID: %v", fake.Removes)
	}
}

// Cleanup releases every still-captured target.
func TestDarwinRouter_Cleanup(t *testing.T) {
	fake := netext.NewFake(true)
	r := newDarwinRouter(Config{}.withDefaults(), fake)
	_ = r.register(1, "com.a")
	_ = r.register(2, "com.b")
	if err := r.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(fake.Removes) != 2 || len(r.ListRouted()) != 0 {
		t.Errorf("Cleanup should release all targets: removes=%v routed=%v", fake.Removes, r.ListRouted())
	}
}
