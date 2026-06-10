// Package platform holds OS-boundary helpers (real-user resolution, filesystem)
// shared by main and the profile store. Dependencies are injected so the logic
// is unit-testable without root or a real home directory.
package platform

import (
	"fmt"
	"os/user"
	"strconv"
)

// RealUser is the invoking (non-root) user, resolved via SUDO_USER when running
// under sudo, so we persist files to their home and chown them back.
type RealUser struct {
	Username string
	Uid      int
	Gid      int
	HomeDir  string
}

// ResolveUser determines the real user. env is os.Getenv; lookup is user.Lookup
// (both injected for tests). Under sudo it uses SUDO_USER; otherwise USER.
func ResolveUser(env func(string) string, lookup func(string) (*user.User, error)) (RealUser, error) {
	name := env("SUDO_USER")
	if name == "" {
		name = env("USER")
	}
	if name == "" || name == "root" {
		return RealUser{}, fmt.Errorf("cannot determine real user (SUDO_USER/USER unset or root)")
	}
	u, err := lookup(name)
	if err != nil {
		return RealUser{}, fmt.Errorf("lookup user %q: %w", name, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return RealUser{}, fmt.Errorf("parse uid %q: %w", u.Uid, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return RealUser{}, fmt.Errorf("parse gid %q: %w", u.Gid, err)
	}
	return RealUser{Username: u.Username, Uid: uid, Gid: gid, HomeDir: u.HomeDir}, nil
}
