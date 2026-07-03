package app

import (
	"os"
	"path/filepath"
	"testing"
)

// tempStore returns a store backed by a fresh temp file (not yet created).
func tempStore(t *testing.T) *appStore {
	t.Helper()
	return newAppStore(filepath.Join(t.TempDir(), "proxied-apps.json"))
}

func TestAppStore_LoadMissingFileIsNotError(t *testing.T) {
	s := tempStore(t)
	if err := s.Load(); err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Errorf("List = %v, want empty", got)
	}
}

func TestAppStore_UpsertPersistsAndRoundTrips(t *testing.T) {
	s := tempStore(t)
	if err := s.Upsert("com.cursor.app", "Cursor", true); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := s.Upsert("com.zen.app", "Zen", false); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	// A fresh store loaded from the same file should see both entries.
	s2 := newAppStore(s.path)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := s2.List()
	if len(got) != 2 {
		t.Fatalf("List after reload = %+v, want 2 entries", got)
	}
	// List is sorted by name: Cursor before Zen.
	if got[0].BundleID != "com.cursor.app" || !got[0].Enabled || got[0].Running {
		t.Errorf("got[0] = %+v, want enabled com.cursor.app, Running=false", got[0])
	}
	if got[1].BundleID != "com.zen.app" || got[1].Enabled {
		t.Errorf("got[1] = %+v, want disabled com.zen.app", got[1])
	}
}

func TestAppStore_UpsertEmptyNameKeepsExisting(t *testing.T) {
	s := tempStore(t)
	_ = s.Upsert("com.cursor.app", "Cursor", true)
	_ = s.Upsert("com.cursor.app", "", false) // re-launch without a resolved name
	got := s.List()
	if len(got) != 1 || got[0].Name != "Cursor" || got[0].Enabled {
		t.Errorf("List = %+v, want name kept, enabled=false", got)
	}
}

func TestAppStore_SetEnabled(t *testing.T) {
	s := tempStore(t)
	if err := s.SetEnabled("com.nope", true); err == nil {
		t.Error("SetEnabled on unknown bundle id should error")
	}
	_ = s.Upsert("com.cursor.app", "Cursor", true)
	if err := s.SetEnabled("com.cursor.app", false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	if ids := s.EnabledBundleIDs(); len(ids) != 0 {
		t.Errorf("EnabledBundleIDs = %v, want empty after disable", ids)
	}
}

func TestAppStore_EnabledBundleIDs(t *testing.T) {
	s := tempStore(t)
	_ = s.Upsert("com.a", "A", true)
	_ = s.Upsert("com.b", "B", false)
	_ = s.Upsert("com.c", "C", true)
	got := s.EnabledBundleIDs()
	if len(got) != 2 || got[0] != "com.a" || got[1] != "com.c" {
		t.Errorf("EnabledBundleIDs = %v, want [com.a com.c]", got)
	}
}

func TestAppStore_Remove(t *testing.T) {
	s := tempStore(t)
	_ = s.Upsert("com.cursor.app", "Cursor", true)
	if err := s.Remove("com.nope"); err != nil { // idempotent
		t.Errorf("Remove unknown id should not error: %v", err)
	}
	if err := s.Remove("com.cursor.app"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Errorf("List after Remove = %v, want empty", got)
	}
}

func TestAppStore_LoadCorruptFileStartsFresh(t *testing.T) {
	s := tempStore(t)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Load(); err != nil {
		t.Fatalf("Load on corrupt file should not error: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Errorf("List after corrupt load = %v, want empty", got)
	}
}
