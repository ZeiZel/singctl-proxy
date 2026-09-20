package sysproxy

import "strings"

// PortHolder describes the process bound to a TCP port that made a pinned
// PAC-server bind fail (see Manager.diagnoseBindError) — F1 item 3 in
// docs/v2-spec.md: a bind failure must be actionable, not just "address
// already in use".
type PortHolder struct {
	PID  int
	Path string
}

// PortDiagnostics is sysproxy's port onto lsof(8)/ps(8)/launchctl(8) — the
// only place (via the real darwin adapter, networksetup_darwin.go, which
// already owns the package's one os/exec import — see
// internal/arch/imports_test.go) this package shells out to identify what
// holds a busy PAC port and to remove singctl's own legacy PAC LaunchAgent.
// Fakeable in tests, the same way NetworkSetup is.
//
// A Manager is usable with no PortDiagnostics at all (see
// Manager.WithPortDiagnostics's doc) — a bind-failure error then simply
// carries no pid/path detail, and ReclaimPort refuses outright, rather than
// panicking.
type PortDiagnostics interface {
	// HolderOf reports the pid/executable path of whatever process is
	// LISTENing on 127.0.0.1:port. ok is false when nothing could be
	// determined (lsof found nothing — e.g. a race with the failed bind — or
	// the lookup itself isn't supported); that is never itself an error,
	// since "couldn't tell who" must not block reporting the original bind
	// failure.
	HolderOf(port int) (holder PortHolder, ok bool, err error)
	// LegacyPACAgent reports whether singctl's own legacy PAC LaunchAgent
	// plist (~/Library/LaunchAgents/com.singctl.pacserver.plist, installed by
	// the old `make pac-server` target — see
	// packaging/macos/com.singctl.pacserver.plist.in) exists on disk and, if
	// so, whether it verifiably matches singctl's own shape (see
	// IsLegacyPACAgentPlist). present is false when no such file exists at
	// all, in which case owned/path are meaningless.
	LegacyPACAgent() (present, owned bool, path string, err error)
	// RemoveLegacyPACAgent boots out and deletes singctl's own legacy PAC
	// LaunchAgent. Implementations MUST re-verify ownership themselves
	// before touching anything on disk (Manager.ReclaimPort already checks
	// first, but this must never rely solely on that) — a foreign
	// LaunchAgent is never removed, no matter who's asking.
	RemoveLegacyPACAgent() error
}

// IsLegacyPACAgentPlist reports whether data is singctl's own legacy PAC
// LaunchAgent plist — the exact criteria
// packaging/macos/scripts/preinstall already applies before it boots the
// agent out (see its step 2): the plist must mention BOTH "http.server" (the
// `python3 -m http.server` ProgramArguments the old `make pac-server` target
// installs — see packaging/macos/com.singctl.pacserver.plist.in) AND
// ".config/singctl" (the --directory it serves out of). Either substring
// alone is not enough — a coincidental match on one is not proof of
// ownership; requiring both together is the same bar preinstall holds itself
// to, and is what makes it safe to boot the agent out (or delete its plist)
// without ever touching a foreign LaunchAgent that happens to share the
// label or the port.
//
// Copied here (as data-driven logic) rather than shelled out to the
// preinstall script, so Manager and tests can run the identical check
// in-process, with no shell and no real plist file required.
func IsLegacyPACAgentPlist(data []byte) bool {
	s := string(data)
	return strings.Contains(s, "http.server") && strings.Contains(s, ".config/singctl")
}
