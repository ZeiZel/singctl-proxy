// Package netext is the singctl (Go) side of the macOS transparent-proxy System
// Extension (see packaging/macos/netextension). The extension catches EVERY
// network stack of a captured app — Chromium, Node/undici, raw sockets — so it
// is the only macOS mechanism that fully proxies Cursor/VS Code per-app, unlike
// the --proxy-server / HTTP_PROXY levers in internal/procproxy which each cover
// only part of such apps (see docs/macos.md).
//
// This package owns the singctl<->extension config contract and a Controller
// port that pushes the set of captured apps (bundle IDs) to the extension. When
// the extension is not installed/approved (or off macOS), the Controller reports
// Available()==false and every mutation is a cheap no-op, so singctl transparently
// falls back to the procproxy env/flag launch path. The only sing-box-style
// platform split is the real darwin Controller vs the no-op elsewhere; the
// config + path logic here is pure and unit-tested.
package netext

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
)

// ExtensionID is the bundle identifier of the system extension; AppGroup is the
// shared container the extension reads config.json from. Kept in sync with
// packaging/macos/netextension (Info.plist / entitlements).
const (
	ExtensionID = "com.singctl.proxy.netext"
	AppGroup    = "group.com.singctl.proxy"
)

// Config is the JSON contract written for the extension. It mirrors
// packaging/macos/netextension/config.example.json byte-for-byte in shape.
type Config struct {
	Targets   []string `json:"targets"`
	SocksHost string   `json:"socksHost"`
	SocksPort int      `json:"socksPort"`
}

// Marshal renders the config as stable, indented JSON with targets sorted, so
// repeated writes of the same set are byte-identical (no spurious file churn /
// no needless reload signals to the extension). Pure.
func (c Config) Marshal() ([]byte, error) {
	out := Config{
		Targets:   append([]string(nil), c.Targets...),
		SocksHost: c.SocksHost,
		SocksPort: c.SocksPort,
	}
	sort.Strings(out.Targets)
	return json.MarshalIndent(out, "", "  ")
}

// Controller pushes the captured-app set to the system extension. AddTarget /
// RemoveTarget are idempotent and persist the new Config when the extension is
// Available; otherwise they are no-ops. Implementations are platform-specific
// (see New): a real darwin controller, a no-op everywhere else.
type Controller interface {
	// Available reports whether the system extension is installed and approved.
	Available() bool
	// AddTarget marks a bundle ID for capture (idempotent).
	AddTarget(bundleID string) error
	// RemoveTarget stops capturing a bundle ID (idempotent).
	RemoveTarget(bundleID string) error
	// Targets returns the current captured set, sorted.
	Targets() []string
}

// BundleInfoPlistPath maps a path that points at (or into) a macOS .app bundle
// to that bundle's Contents/Info.plist. It accepts a bundle path
// ("/Applications/Cursor.app"), an executable inside it
// (".../Cursor.app/Contents/MacOS/Cursor"), or anything in between, and returns
// "" when no ".app" ancestor is found. Pure, so it is unit-tested and shared by
// the darwin BundleID resolver.
func BundleInfoPlistPath(p string) string {
	p = filepath.Clean(p)
	for p != "/" && p != "." && p != "" {
		if strings.HasSuffix(p, ".app") {
			return filepath.Join(p, "Contents", "Info.plist")
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return ""
}

// targetSet is a pure, sorted, deduped set of bundle IDs shared by the real and
// fake controllers. Not safe for concurrent use; callers serialise access.
type targetSet struct{ m map[string]bool }

func newTargetSet() *targetSet { return &targetSet{m: map[string]bool{}} }

// add inserts id and reports whether the set changed.
func (s *targetSet) add(id string) bool {
	if id == "" || s.m[id] {
		return false
	}
	s.m[id] = true
	return true
}

// remove deletes id and reports whether the set changed.
func (s *targetSet) remove(id string) bool {
	if !s.m[id] {
		return false
	}
	delete(s.m, id)
	return true
}

func (s *targetSet) list() []string {
	out := make([]string, 0, len(s.m))
	for id := range s.m {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
