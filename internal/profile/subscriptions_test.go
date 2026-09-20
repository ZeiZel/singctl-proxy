package profile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"singctl/internal/sub"
)

// modeFakeFS is like fakeFS (store_test.go) but also records the mode each
// file was written with, so subscriptions.json's 0600 permission can be
// asserted without changing the shared fakeFS in store_test.go.
type modeFakeFS struct {
	files  map[string][]byte
	modes  map[string]os.FileMode
	chowns []chownCall
}

func newModeFakeFS() *modeFakeFS {
	return &modeFakeFS{files: map[string][]byte{}, modes: map[string]os.FileMode{}}
}

func (f *modeFakeFS) MkdirAll(string, os.FileMode) error { return nil }
func (f *modeFakeFS) WriteFile(n string, d []byte, perm os.FileMode) error {
	f.files[n] = append([]byte(nil), d...)
	f.modes[n] = perm
	return nil
}
func (f *modeFakeFS) ReadFile(n string) ([]byte, error) {
	d, ok := f.files[n]
	if !ok {
		return nil, os.ErrNotExist
	}
	return d, nil
}
func (f *modeFakeFS) Chown(n string, uid, gid int) error {
	f.chowns = append(f.chowns, chownCall{n, uid, gid})
	return nil
}
func (f *modeFakeFS) Remove(n string) error {
	if _, ok := f.files[n]; !ok {
		return os.ErrNotExist
	}
	delete(f.files, n)
	return nil
}

func TestStore_Subscriptions_RoundTrip(t *testing.T) {
	fs := newFakeFS()
	s := NewStore(fs, "/Users/mikhail", 501, 20)

	added := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC)
	expire := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	want := []sub.Subscription{
		{
			URL:        "https://panel.example.com/sub/abc",
			Title:      "Main",
			AddedAt:    added,
			LastUpdate: updated,
			Links: []string{
				"vless://uuid@host1:443",
				"vless://uuid@host2:443",
			},
			Meta: sub.Meta{
				Title:          "Main",
				UpdateInterval: 6 * time.Hour,
				Upload:         100,
				Download:       200,
				Total:          1024 * 1024 * 1024,
				Expire:         expire,
			},
		},
		{
			URL:       "https://panel.example.com/sub/def",
			AddedAt:   added,
			LastError: "connection refused",
		},
	}

	if err := s.SaveSubscriptions(want); err != nil {
		t.Fatal(err)
	}

	got, err := s.LoadSubscriptions()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip mismatch:\n got  %#v\n want %#v", got, want)
	}

	// Order must be preserved exactly as given (priority order).
	if got[0].URL != want[0].URL || got[1].URL != want[1].URL {
		t.Fatal("subscription order not preserved")
	}
}

func TestStore_Subscriptions_MissingFile(t *testing.T) {
	fs := newFakeFS()
	s := NewStore(fs, "/home/u", 1000, 1000)

	got, err := s.LoadSubscriptions()
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("missing file should yield empty slice, got %v", got)
	}
}

func TestStore_Subscriptions_CorruptFile(t *testing.T) {
	fs := newFakeFS()
	s := NewStore(fs, "/home/u", 1000, 1000)

	wantPath := filepath.Join("/home/u", ".config", "singctl", "subscriptions.json")
	fs.files[wantPath] = []byte("{not valid json")

	_, err := s.LoadSubscriptions()
	if err == nil {
		t.Fatal("corrupt file should return an error")
	}
	if !strings.Contains(err.Error(), wantPath) {
		t.Errorf("error %q should name the bad file %q so the user can delete it", err.Error(), wantPath)
	}
}

func TestStore_Subscriptions_ClearingRemovesFile(t *testing.T) {
	fs := newFakeFS()
	s := NewStore(fs, "/home/u", 1000, 1000)

	subs := []sub.Subscription{{URL: "https://panel.example.com/sub/abc"}}
	if err := s.SaveSubscriptions(subs); err != nil {
		t.Fatal(err)
	}

	wantPath := filepath.Join("/home/u", ".config", "singctl", "subscriptions.json")
	if _, ok := fs.files[wantPath]; !ok {
		t.Fatalf("expected %s to exist after save", wantPath)
	}

	// Clearing with nil should remove the file rather than leave "[]" behind.
	if err := s.SaveSubscriptions(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.files[wantPath]; ok {
		t.Fatalf("expected %s to be removed after clearing", wantPath)
	}

	// Clearing again (already absent) must not error.
	if err := s.SaveSubscriptions(nil); err != nil {
		t.Fatalf("clearing an already-empty store should not error, got %v", err)
	}

	// Clearing with an empty (non-nil) slice behaves the same.
	if err := s.SaveSubscriptions(subs); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSubscriptions([]sub.Subscription{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.files[wantPath]; ok {
		t.Fatalf("expected %s to be removed after clearing with empty slice", wantPath)
	}
}

func TestStore_Subscriptions_ChownAndMode(t *testing.T) {
	fs := newModeFakeFS()
	s := NewStore(fs, "/Users/mikhail", 501, 20)

	subs := []sub.Subscription{{URL: "https://panel.example.com/sub/abc"}}
	if err := s.SaveSubscriptions(subs); err != nil {
		t.Fatal(err)
	}

	wantPath := filepath.Join("/Users/mikhail", ".config", "singctl", "subscriptions.json")
	wantDir := filepath.Join("/Users/mikhail", ".config", "singctl")

	var dirChowned, fileChowned bool
	for _, c := range fs.chowns {
		if c.uid != 501 || c.gid != 20 {
			t.Errorf("chown to %d:%d, want 501:20", c.uid, c.gid)
		}
		if c.name == wantDir {
			dirChowned = true
		}
		if c.name == wantPath {
			fileChowned = true
		}
	}
	if !dirChowned {
		t.Error("subscriptions dir was not chowned back to the real user")
	}
	if !fileChowned {
		t.Error("subscriptions file was not chowned back to the real user")
	}

	if mode, ok := fs.modes[wantPath]; !ok {
		t.Fatalf("subscriptions file %s was never written", wantPath)
	} else if mode != 0o600 {
		t.Errorf("subscriptions file mode = %o, want 0600", mode)
	}
}
