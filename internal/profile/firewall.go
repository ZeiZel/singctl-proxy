package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"singctl/internal/firewall"
)

// F6 item 5 in docs/v2-spec.md: firewall rules get the same on-disk home and
// SUDO_USER chown discipline as subscriptions.json (see subscriptions.go) —
// this file mirrors that pattern rather than the byte-oriented sysproxy.ini
// one, because firewall.Rule is already a plain typed value with no
// engine-specific wire format the caller needs to own.

func (s *Store) firewallPath() string { return filepath.Join(s.dir, "firewall.json") }

// LoadFirewallRules returns the persisted firewall rules, in the order they
// were added. A missing file is not an error — no rules have ever been
// configured — and returns an empty slice. A corrupt file returns an error
// naming the file, so the caller can report it instead of the daemon getting
// stuck on startup (mirrors LoadSubscriptions).
func (s *Store) LoadFirewallRules() ([]firewall.Rule, error) {
	data, err := s.fs.ReadFile(s.firewallPath())
	if err != nil {
		if os.IsNotExist(err) {
			return []firewall.Rule{}, nil
		}
		return nil, err
	}
	var rules []firewall.Rule
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, fmt.Errorf("profile: %s is corrupt (%w); delete it to reset firewall rules", s.firewallPath(), err)
	}
	return rules, nil
}

// SaveFirewallRules persists rules in the given order, pretty-printed, mode
// 0600, chowned back to the real user like the rest of the store. Passing nil
// or an empty slice removes the file rather than leaving an empty array
// behind (mirrors SaveSubscriptions).
func (s *Store) SaveFirewallRules(rules []firewall.Rule) error {
	if err := s.fs.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	if len(rules) == 0 {
		if err := s.fs.Remove(s.firewallPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		_ = s.fs.Chown(s.dir, s.uid, s.gid)
		return nil
	}
	data, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return err
	}
	if err := s.fs.WriteFile(s.firewallPath(), data, 0o600); err != nil {
		return err
	}
	_ = s.fs.Chown(s.dir, s.uid, s.gid)
	_ = s.fs.Chown(s.firewallPath(), s.uid, s.gid)
	return nil
}
