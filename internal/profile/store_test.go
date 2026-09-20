package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type chownCall struct {
	name     string
	uid, gid int
}

type fakeFS struct {
	files  map[string][]byte
	chowns []chownCall
}

func newFakeFS() *fakeFS { return &fakeFS{files: map[string][]byte{}} }

func (f *fakeFS) MkdirAll(string, os.FileMode) error { return nil }
func (f *fakeFS) WriteFile(n string, d []byte, _ os.FileMode) error {
	f.files[n] = append([]byte(nil), d...)
	return nil
}
func (f *fakeFS) ReadFile(n string) ([]byte, error) {
	d, ok := f.files[n]
	if !ok {
		return nil, os.ErrNotExist
	}
	return d, nil
}
func (f *fakeFS) Chown(n string, uid, gid int) error {
	f.chowns = append(f.chowns, chownCall{n, uid, gid})
	return nil
}
func (f *fakeFS) Remove(n string) error {
	if _, ok := f.files[n]; !ok {
		return os.ErrNotExist
	}
	delete(f.files, n)
	return nil
}

func TestStore_Save_WritesUnderRealUserHome_AndChowns(t *testing.T) {
	fs := newFakeFS()
	s := NewStore(fs, "/Users/mikhail", 501, 20)
	if err := s.Save("vless://uuid@host:443"); err != nil {
		t.Fatal(err)
	}

	wantPath := filepath.Join("/Users/mikhail", ".config", "singctl", "profile.txt")
	if got, ok := fs.files[wantPath]; !ok || string(got) != "vless://uuid@host:443" {
		t.Fatalf("profile not written to %s (files: %v)", wantPath, fs.files)
	}
	// Both the dir and the file must be chowned back to the real user.
	var fileChowned bool
	for _, c := range fs.chowns {
		if c.uid != 501 || c.gid != 20 {
			t.Errorf("chown to %d:%d, want 501:20", c.uid, c.gid)
		}
		if c.name == wantPath {
			fileChowned = true
		}
	}
	if !fileChowned {
		t.Error("profile file was not chowned back to the real user")
	}
	if !strings.Contains(wantPath, "/Users/mikhail/") {
		t.Error("must write under the real user's home")
	}
}

func TestStore_Load_RoundTrip_AndMissing(t *testing.T) {
	fs := newFakeFS()
	s := NewStore(fs, "/home/u", 1000, 1000)

	if got, err := s.Load(); err != nil || got != "" {
		t.Fatalf("empty load = (%q,%v), want (\"\",nil)", got, err)
	}
	if err := s.Save("  vless://x@y:1  "); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != "vless://x@y:1" {
		t.Errorf("loaded %q, want trimmed link", got)
	}
}

func TestAutostartMode_DefaultsToOffAndRoundTrips(t *testing.T) {
	fs := newFakeFS()
	s := NewStore(fs, "/home/u", 1000, 1000)

	// F2 item 2: a fresh install (nothing ever saved) must default to "off",
	// never re-derive an old --vpn flag or any other implicit mode.
	got, err := s.LoadAutostartMode()
	if err != nil || got != "off" {
		t.Fatalf("fresh LoadAutostartMode = (%q,%v), want (\"off\",nil)", got, err)
	}

	if err := s.SaveAutostartMode("vpn"); err != nil {
		t.Fatalf("SaveAutostartMode: %v", err)
	}
	got, err = s.LoadAutostartMode()
	if err != nil || got != "vpn" {
		t.Fatalf("LoadAutostartMode after save = (%q,%v), want (\"vpn\",nil)", got, err)
	}
}

func TestIntroMarker(t *testing.T) {
	fs := newFakeFS()
	s := NewStore(fs, "/home/u", 1000, 1000)
	if s.HasSeenIntro() {
		t.Fatal("fresh store should not have seen the intro")
	}
	if err := s.MarkIntroSeen(); err != nil {
		t.Fatalf("MarkIntroSeen: %v", err)
	}
	if !s.HasSeenIntro() {
		t.Error("after MarkIntroSeen, HasSeenIntro should be true")
	}
}
