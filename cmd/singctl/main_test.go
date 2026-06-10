package main

import "testing"

func TestRequireRoot(t *testing.T) {
	if err := requireRoot("darwin", 0); err != nil {
		t.Errorf("euid 0 (root) must be allowed, got %v", err)
	}
	if err := requireRoot("darwin", 501); err == nil {
		t.Error("non-root euid must be rejected")
	}
	if err := requireRoot("linux", 501); err == nil {
		t.Error("non-root euid must be rejected on linux too")
	}
	// Windows has no euid (os.Geteuid() == -1); the check must not lock the
	// binary out there.
	if err := requireRoot("windows", -1); err != nil {
		t.Errorf("windows must skip the euid check, got %v", err)
	}
}
