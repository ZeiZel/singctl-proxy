package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRotateLogIfLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "singbox.log")

	// Below cap → untouched, no .1 created.
	if err := os.WriteFile(path, []byte("small"), 0o644); err != nil {
		t.Fatal(err)
	}
	rotateLogIfLarge(path, 1024, -1, -1)
	if _, err := os.Stat(path + ".1"); err == nil {
		t.Error("small log must not be rotated")
	}
	if b, _ := os.ReadFile(path); string(b) != "small" {
		t.Error("small log must be left intact")
	}

	// Over cap → rolled over to .1, original path cleared for a fresh file.
	big := make([]byte, 2048)
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatal(err)
	}
	rotateLogIfLarge(path, 1024, -1, -1)
	if fi, err := os.Stat(path + ".1"); err != nil || fi.Size() != 2048 {
		t.Fatalf("over-cap log must be rolled to .1 (size 2048), err=%v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("original path should be gone after rename (recreated by the caller)")
	}
}
