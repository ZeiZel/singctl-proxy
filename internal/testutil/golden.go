// Package testutil holds cross-cutting test helpers (golden-file assertions,
// fakes, scenario builders). It guarantees the unit suite runs without root,
// without a real sing-box tunnel, and without touching Cisco.
package testutil

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files instead of comparing")

// AssertGoldenJSON compares got against the golden file at path (relative to the
// calling test's package dir, e.g. "testdata/x.golden.json"). Run the suite with
// -update to (re)generate golden files.
func AssertGoldenJSON(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir golden dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run `go test -update` to create)", path, err)
	}
	if string(got) != string(want) {
		t.Errorf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}
