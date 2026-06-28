package netext

import (
	"encoding/json"
	"testing"
)

func TestConfigMarshal_StableSorted(t *testing.T) {
	a, err := Config{Targets: []string{"com.b", "com.a"}, SocksHost: "127.0.0.1", SocksPort: 1080}.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Same set in a different order must produce identical bytes (sorted).
	b, _ := Config{Targets: []string{"com.a", "com.b"}, SocksHost: "127.0.0.1", SocksPort: 1080}.Marshal()
	if string(a) != string(b) {
		t.Errorf("marshal not order-stable:\n%s\n---\n%s", a, b)
	}
	// Round-trips and keeps the contract field names.
	var got Config
	if err := json.Unmarshal(a, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Targets) != 2 || got.Targets[0] != "com.a" || got.SocksPort != 1080 || got.SocksHost != "127.0.0.1" {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// The Cisco-yield flag survives the round-trip (extension reads it to yield).
	withCisco, _ := Config{Targets: []string{"com.a"}, SocksHost: "127.0.0.1", SocksPort: 1080, CiscoActive: true}.Marshal()
	var gotCisco Config
	if err := json.Unmarshal(withCisco, &gotCisco); err != nil {
		t.Fatalf("unmarshal cisco: %v", err)
	}
	if !gotCisco.CiscoActive {
		t.Errorf("CiscoActive lost in round-trip: %+v", gotCisco)
	}
}

func TestBundleInfoPlistPath(t *testing.T) {
	cases := map[string]string{
		"/Applications/Cursor.app":                                     "/Applications/Cursor.app/Contents/Info.plist",
		"/Applications/Cursor.app/Contents/MacOS/Cursor":               "/Applications/Cursor.app/Contents/Info.plist",
		"/Applications/Visual Studio Code.app/Contents/MacOS/Electron": "/Applications/Visual Studio Code.app/Contents/Info.plist",
		"/usr/bin/curl": "",
		"":              "",
		"/":             "",
	}
	for in, want := range cases {
		if got := BundleInfoPlistPath(in); got != want {
			t.Errorf("BundleInfoPlistPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTargetSet(t *testing.T) {
	s := newTargetSet()
	if !s.add("com.a") || s.add("com.a") {
		t.Error("add should report change only on first insert")
	}
	if s.add("") {
		t.Error("empty id should not be added")
	}
	s.add("com.b")
	got := s.list()
	if len(got) != 2 || got[0] != "com.a" || got[1] != "com.b" {
		t.Errorf("list = %v, want sorted [com.a com.b]", got)
	}
	if !s.remove("com.a") || s.remove("com.a") {
		t.Error("remove should report change only on first delete")
	}
}

// On the build/test platform (non-darwin in CI), the controller is the no-op:
// Available is false and mutations are inert. BundleID is unresolved.
func TestNewController_Platform(t *testing.T) {
	c := New("127.0.0.1", 1080)
	if err := c.AddTarget("com.x"); err != nil {
		t.Errorf("AddTarget should not error: %v", err)
	}
	// A no-op controller (non-darwin) stays empty and unavailable; the real
	// darwin one is unavailable in CI too (extension not installed). Either way
	// Available must not panic and the path resolver must be callable.
	_ = c.Available()
	_ = BundleID("/Applications/Cursor.app")
	_ = BundleIDForPID(0)
}

func TestFakeController(t *testing.T) {
	f := NewFake(true)
	if !f.Available() {
		t.Error("NewFake(true) should be Available")
	}
	_ = f.AddTarget("com.a")
	_ = f.AddTarget("com.b")
	_ = f.RemoveTarget("com.a")
	if len(f.Adds) != 2 || len(f.Removes) != 1 {
		t.Errorf("call recording wrong: adds=%v removes=%v", f.Adds, f.Removes)
	}
	if got := f.Targets(); len(got) != 1 || got[0] != "com.b" {
		t.Errorf("Targets = %v, want [com.b]", got)
	}
}
