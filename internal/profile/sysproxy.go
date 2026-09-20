package profile

import (
	"os"
	"path/filepath"
)

// F1b in docs/v2-spec.md: the applied system-proxy (PAC) config used to live
// only in internal/sysproxy's Manager, in memory — nothing wrote it, nothing
// restored it, so a daemon restart silently left macOS pointed at a dead PAC
// URL. This file gives it the same on-disk home (and root-daemon/SUDO_USER
// chown discipline) as the rest of the store.
//
// The store is deliberately byte-oriented rather than typed on
// sysproxy.Config: this package stays free of an internal/sysproxy import,
// exactly like SaveAutostartMode stays free of typing its string against a
// sysproxy-side enum. The caller (internal/app.Executor) is the one that
// knows the wire format is sysproxy.Config.INI/ParseINI.

func (s *Store) sysproxyPath() string { return filepath.Join(s.dir, "sysproxy.ini") }

// SaveSysproxyConfig persists data (the INI rendering of the applied
// sysproxy.Config — see Config.INI) next to the rest of the store state,
// chowned back to the real user like everything else here. Called after
// every successful SYSPROXY-SET/SYSPROXY-IMPORT, including one that lands on
// mode = off, so a user who explicitly turns the proxy off does not have it
// switched back on by the next restore (F1b).
func (s *Store) SaveSysproxyConfig(data []byte) error {
	if err := s.fs.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if err := s.fs.WriteFile(s.sysproxyPath(), data, 0o600); err != nil {
		return err
	}
	_ = s.fs.Chown(s.dir, s.uid, s.gid)
	_ = s.fs.Chown(s.sysproxyPath(), s.uid, s.gid)
	return nil
}

// LoadSysproxyConfig returns the persisted sysproxy INI bytes, or nil if none
// has ever been saved — a fresh install, or one from before F1b. That is not
// an error: the caller treats a nil/empty result as "nothing to restore."
func (s *Store) LoadSysproxyConfig() ([]byte, error) {
	data, err := s.fs.ReadFile(s.sysproxyPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}
