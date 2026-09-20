// Package profile persists the user's share link under the REAL user's home
// (resolved via SUDO_USER) and chowns files back to them, so running under sudo
// never leaves root-owned files. The filesystem is injected for testing.
package profile

import (
	"os"
	"path/filepath"
	"strings"
)

// FS is the minimal filesystem surface the store needs.
type FS interface {
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(name string, data []byte, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	Chown(name string, uid, gid int) error
	Remove(name string) error
}

// Store reads/writes a single saved profile link.
type Store struct {
	fs       FS
	dir      string
	uid, gid int
}

// NewStore targets <homeDir>/.config/singctl, chowning to uid/gid.
func NewStore(fs FS, homeDir string, uid, gid int) *Store {
	return &Store{
		fs:  fs,
		dir: filepath.Join(homeDir, ".config", "singctl"),
		uid: uid,
		gid: gid,
	}
}

func (s *Store) path() string          { return filepath.Join(s.dir, "profile.txt") }
func (s *Store) introPath() string     { return filepath.Join(s.dir, "intro-shown") }
func (s *Store) autostartPath() string { return filepath.Join(s.dir, "autostart-mode.txt") }

// SaveAutostartMode persists the autostart mode (F2 item 2: "off"|"proxy"|
// "vpn", chosen in Settings and applied by the daemon at the next startup)
// and chowns it back to the real user like the saved profile.
func (s *Store) SaveAutostartMode(mode string) error {
	if err := s.fs.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := s.fs.WriteFile(s.autostartPath(), []byte(mode), 0o600); err != nil {
		return err
	}
	_ = s.fs.Chown(s.dir, s.uid, s.gid)
	_ = s.fs.Chown(s.autostartPath(), s.uid, s.gid)
	return nil
}

// LoadAutostartMode returns the persisted autostart mode, or "off" if none has
// ever been saved (a fresh install, or one from before F2).
func (s *Store) LoadAutostartMode() (string, error) {
	data, err := s.fs.ReadFile(s.autostartPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "off", nil
		}
		return "off", err
	}
	mode := strings.TrimSpace(string(data))
	if mode == "" {
		return "off", nil
	}
	return mode, nil
}

// HasSeenIntro reports whether the first-run intro animation has already played.
func (s *Store) HasSeenIntro() bool {
	_, err := s.fs.ReadFile(s.introPath())
	return err == nil
}

// MarkIntroSeen records that the intro has played (best-effort), chowning the
// marker back to the real user like the saved profile.
func (s *Store) MarkIntroSeen() error {
	if err := s.fs.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := s.fs.WriteFile(s.introPath(), []byte("1"), 0o600); err != nil {
		return err
	}
	_ = s.fs.Chown(s.dir, s.uid, s.gid)
	_ = s.fs.Chown(s.introPath(), s.uid, s.gid)
	return nil
}

// Save writes the link and chowns the dir + file back to the real user.
func (s *Store) Save(link string) error {
	if err := s.fs.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := s.fs.WriteFile(s.path(), []byte(link), 0o600); err != nil {
		return err
	}
	// Best-effort chown so sudo doesn't leave root-owned files.
	_ = s.fs.Chown(s.dir, s.uid, s.gid)
	_ = s.fs.Chown(s.path(), s.uid, s.gid)
	return nil
}

// Load returns the saved link, or "" if none.
func (s *Store) Load() (string, error) {
	data, err := s.fs.ReadFile(s.path())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// OSFS is the real filesystem.
type OSFS struct{}

func (OSFS) MkdirAll(p string, perm os.FileMode) error            { return os.MkdirAll(p, perm) }
func (OSFS) WriteFile(n string, d []byte, perm os.FileMode) error { return os.WriteFile(n, d, perm) }
func (OSFS) ReadFile(n string) ([]byte, error)                    { return os.ReadFile(n) }
func (OSFS) Chown(n string, uid, gid int) error                   { return os.Chown(n, uid, gid) }
func (OSFS) Remove(n string) error                                { return os.Remove(n) }
