package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// proxiedAppsPath is the root-writable, PID-independent home of the persistent
// per-app proxy store: what the GUI's Apps tab shows and enables/disables
// survives daemon restarts (unlike the router's PID/bundle bookkeeping, which
// only exists while a process is actually running). The daemon runs as root
// (see procproxy.startProcess's sudo drop), so a location under
// /Library/Application Support is writable and, unlike a per-user path, is not
// tied to whichever user happens to be logged in when the daemon starts.
const proxiedAppsPath = "/Library/Application Support/singctl/proxied-apps.json"

// ProxiedApp is one entry in the persistent per-app proxy store: its bundle
// ID, a display name, and whether it is enabled for capture. JSON tags mirror
// gui/bridge/app.go's ProxiedApp exactly (the shared contract), so a
// []ProxiedApp can be marshalled straight into a control-socket response the
// bridge decodes without translation.
//
// Running is NOT meaningful in a stored/loaded copy — it is always false on
// disk and on Load; Executor.ListProxiedApps fills it in at list time from the
// live process table (see bundlePIDs), which is the only place "currently
// running" can be answered from.
type ProxiedApp struct {
	BundleID string `json:"bundleID"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Running  bool   `json:"running"`
}

// appStore is the mutex-guarded, JSON-file-backed persistent store of proxied
// apps. It never touches the netext.Controller itself — enable/disable/remove
// are pure store mutations, and the Executor's RecomputeAppTargets is the
// single place that turns the store's enabled set (plus anything currently
// PID-routed) into a controller.SetTargets call. Keeping the store ignorant of
// the controller means it has no partial-failure modes to reconcile: a store
// write either lands on disk or it doesn't, and the controller catches up on
// the next recompute.
type appStore struct {
	path string

	mu      sync.Mutex
	entries map[string]ProxiedApp // bundleID -> entry
}

// newAppStore returns a store backed by path (proxiedAppsPath in production;
// tests inject a temp file path).
func newAppStore(path string) *appStore {
	return &appStore{path: path, entries: map[string]ProxiedApp{}}
}

// Load reads the store file if present. A missing file is not an error (fresh
// install — nothing has ever been proxied yet); a corrupt file is also
// swallowed to a fresh, empty store rather than failing daemon startup over a
// non-critical cache of GUI convenience state.
func (s *appStore) Load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("appstore: чтение %s: %w", s.path, err)
	}
	var list []ProxiedApp
	if err := json.Unmarshal(data, &list); err != nil {
		return nil // corrupt file — start fresh rather than fail startup
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[string]ProxiedApp, len(list))
	for _, e := range list {
		e.Running = false
		if e.BundleID == "" {
			continue
		}
		s.entries[e.BundleID] = e
	}
	return nil
}

// List returns every stored entry, sorted by name (case-insensitive). Running
// is always false — callers derive it (see Executor.ListProxiedApps).
func (s *appStore) List() []ProxiedApp {
	s.mu.Lock()
	out := make([]ProxiedApp, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// EnabledBundleIDs returns a sorted snapshot of the bundle IDs currently
// enabled in the store, for Executor.RecomputeAppTargets' union.
func (s *appStore) EnabledBundleIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.entries))
	for id, e := range s.entries {
		if e.Enabled {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// Upsert inserts or updates the entry for bundleID: name replaces the stored
// display name when non-empty (a re-launch of an already-known app keeps its
// existing name if the caller didn't resolve a fresh one), and enabled always
// overwrites (the caller states the app's enabled state explicitly).
func (s *appStore) Upsert(bundleID, name string, enabled bool) error {
	bundleID = strings.TrimSpace(bundleID)
	if bundleID == "" {
		return fmt.Errorf("appstore: пустой bundle id")
	}
	s.mu.Lock()
	e := s.entries[bundleID]
	e.BundleID = bundleID
	if name != "" {
		e.Name = name
	}
	e.Enabled = enabled
	s.entries[bundleID] = e
	s.mu.Unlock()
	return s.save()
}

// SetEnabled flips the enabled flag of an existing entry. Errors if bundleID
// isn't in the store (nothing to enable/disable).
func (s *appStore) SetEnabled(bundleID string, enabled bool) error {
	bundleID = strings.TrimSpace(bundleID)
	s.mu.Lock()
	e, ok := s.entries[bundleID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("appstore: приложение %s не найдено", bundleID)
	}
	e.Enabled = enabled
	s.entries[bundleID] = e
	s.mu.Unlock()
	return s.save()
}

// Remove deletes bundleID from the store (idempotent — removing an unknown
// bundle ID is not an error).
func (s *appStore) Remove(bundleID string) error {
	s.mu.Lock()
	delete(s.entries, strings.TrimSpace(bundleID))
	s.mu.Unlock()
	return s.save()
}

// save persists the current entries as stable, sorted-by-bundleID indented
// JSON, written atomically via temp+rename so a reader (or a crash mid-write)
// never observes a half-written file.
func (s *appStore) save() error {
	s.mu.Lock()
	list := make([]ProxiedApp, 0, len(s.entries))
	for _, e := range s.entries {
		list = append(list, e)
	}
	s.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].BundleID < list[j].BundleID })

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("appstore: marshal: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("appstore: создать каталог %s: %w", dir, err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("appstore: записать %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("appstore: rename %s -> %s: %w", tmp, s.path, err)
	}
	return nil
}
