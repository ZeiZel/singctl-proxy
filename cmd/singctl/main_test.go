package main

import "testing"

func TestRequireRoot(t *testing.T) {
	if err := requireRoot(0); err != nil {
		t.Errorf("euid 0 (root) must be allowed, got %v", err)
	}
	if err := requireRoot(501); err == nil {
		t.Error("non-root euid must be rejected")
	}
}
