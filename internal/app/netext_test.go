package app

import (
	"testing"

	"singctl/internal/netext"
)

// captureExtension/releaseExtension gate on the controller being present AND the
// extension being Available, and ignore empty bundle IDs. This is the seam that
// keeps the macOS extension wiring a safe no-op until the extension is installed.
func TestCaptureExtension_Gating(t *testing.T) {
	e := &Executor{}

	// nil controller: must not panic, nothing recorded.
	e.captureExtension("com.x")
	e.releaseExtension("com.x")

	// Controller present but extension not approved: no capture.
	off := netext.NewFake(false)
	e.netext = off
	e.captureExtension("com.cursor")
	if len(off.Adds) != 0 {
		t.Errorf("unavailable extension must not capture, got %v", off.Adds)
	}

	// Available extension: empty id ignored, real id captured then released.
	on := netext.NewFake(true)
	e.netext = on
	e.captureExtension("")
	e.captureExtension("com.cursor")
	if len(on.Adds) != 1 || on.Adds[0] != "com.cursor" {
		t.Errorf("expected one capture of com.cursor, got %v", on.Adds)
	}
	e.releaseExtension("")
	e.releaseExtension("com.cursor")
	if len(on.Removes) != 1 || on.Removes[0] != "com.cursor" {
		t.Errorf("expected one release of com.cursor, got %v", on.Removes)
	}
}
