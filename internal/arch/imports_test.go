package arch

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moduleRoot walks up from the test's working directory (the package dir) until
// it finds go.mod, returning the module root.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from CWD")
		}
		dir = parent
	}
}

// goFiles returns every .go file under root, skipping vendored/generated dirs.
func goFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", "testdata", ".git", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	return files
}

// execAllowed reports whether a file is an approved place to import os/exec.
// Only real OS adapters may shell out; pure logic packages must not.
func execAllowed(rel string) bool {
	base := filepath.Base(rel)
	return strings.HasSuffix(base, "_real.go") ||
		strings.HasSuffix(base, "_darwin.go") ||
		strings.HasSuffix(base, "_other.go") || // portable (!darwin) OS adapters
		strings.Contains(rel, filepath.FromSlash("internal/platform/"))
}

// singboxAllowed reports whether a file is an approved place to import sing-box.
// The embedded core is isolated behind internal/core.
func singboxAllowed(rel string) bool {
	return strings.Contains(rel, filepath.FromSlash("internal/core/"))
}

// TestNoForbiddenImports enforces the architecture boundaries from PLAN.md §8:
// sing-box is importable only inside internal/core, and os/exec only inside the
// real OS adapters. This makes "fully mockable" a structural guarantee.
func TestNoForbiddenImports(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	for _, file := range goFiles(t, root) {
		rel, err := filepath.Rel(root, file)
		if err != nil {
			t.Fatalf("rel %s: %v", file, err)
		}
		f, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			switch {
			case path == "os/exec":
				if !execAllowed(rel) {
					t.Errorf("%s imports os/exec but is not an approved OS adapter "+
						"(allowed: *_real.go, *_darwin.go, *_other.go, internal/platform/...)", rel)
				}
			case strings.HasPrefix(path, "github.com/sagernet/sing-box"):
				if !singboxAllowed(rel) {
					t.Errorf("%s imports sing-box (%s) outside internal/core", rel, path)
				}
			}
		}
	}
}

// TestNoCiscoBinaryReferences guarantees the tool never invokes Cisco binaries:
// no production source may reference Cisco's install path. Test files are
// exempt (fixtures/docs may legitimately mention the path).
func TestNoCiscoBinaryReferences(t *testing.T) {
	root := moduleRoot(t)
	const ciscoPath = "/opt/" + "cisco" // split so this guard file isn't its own hit
	for _, file := range goFiles(t, root) {
		rel, err := filepath.Rel(root, file)
		if err != nil {
			t.Fatalf("rel %s: %v", file, err)
		}
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if strings.Contains(string(data), ciscoPath) {
			t.Errorf("%s references %s — Cisco binaries must never be invoked "+
				"(passive observation only)", rel, ciscoPath)
		}
	}
}
