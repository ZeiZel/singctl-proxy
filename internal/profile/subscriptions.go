package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"singctl/internal/sub"
)

func (s *Store) subscriptionsPath() string { return filepath.Join(s.dir, "subscriptions.json") }

// LoadSubscriptions returns the persisted subscriptions in the order they were
// saved. A missing file is not an error — it means no subscriptions have been
// configured yet — and returns an empty slice. A corrupt file returns an error
// naming the file, so the caller can report it (and the user can delete it)
// instead of the daemon getting stuck on startup.
func (s *Store) LoadSubscriptions() ([]sub.Subscription, error) {
	data, err := s.fs.ReadFile(s.subscriptionsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return []sub.Subscription{}, nil
		}
		return nil, err
	}
	var subs []sub.Subscription
	if err := json.Unmarshal(data, &subs); err != nil {
		return nil, fmt.Errorf("profile: %s is corrupt (%w); delete it to reset subscriptions", s.subscriptionsPath(), err)
	}
	return subs, nil
}

// SaveSubscriptions persists subs in the given order (the user's priority
// order), pretty-printed, mode 0600, chowned back to the real user like the
// rest of the store. Passing nil or an empty slice removes the file rather
// than leaving an empty array behind.
func (s *Store) SaveSubscriptions(subs []sub.Subscription) error {
	if err := s.fs.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if len(subs) == 0 {
		if err := s.fs.Remove(s.subscriptionsPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		_ = s.fs.Chown(s.dir, s.uid, s.gid)
		return nil
	}
	data, err := json.MarshalIndent(subs, "", "  ")
	if err != nil {
		return err
	}
	if err := s.fs.WriteFile(s.subscriptionsPath(), data, 0o600); err != nil {
		return err
	}
	_ = s.fs.Chown(s.dir, s.uid, s.gid)
	_ = s.fs.Chown(s.subscriptionsPath(), s.uid, s.gid)
	return nil
}
