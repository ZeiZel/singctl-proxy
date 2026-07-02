package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"regexp"
)

// ioregUUIDPattern extracts the quoted value of "IOPlatformUUID" = "..." from
// `ioreg -rd1 -c IOPlatformExpertDevice` output.
var ioregUUIDPattern = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([0-9A-Za-z-]+)"`)

// DeviceID returns a stable, opaque identifier for this machine: the
// lowercase hex SHA-256 of the macOS IOPlatformUUID (read via `ioreg`, which
// works whether the caller is root — the daemon — or an unprivileged user —
// the GUI/CLI). It never returns the raw UUID: only its hash, so the value is
// safe to send to the license server without exposing real hardware identity.
//
// Deliberately dependency-light (os/exec + crypto/sha256 only) since this is
// used from both the CLI and the GUI bridge. On any failure to run ioreg or
// find the UUID, it returns "" — callers must treat an empty device id as
// "unknown device" rather than failing hard, since license enforcement should
// never be bricked by a platform quirk on an unexpected macOS variant.
func DeviceID() string {
	uuid := platformUUID()
	if uuid == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(uuid))
	return hex.EncodeToString(sum[:])
}

// platformUUID shells out to ioreg and extracts IOPlatformUUID's value, or ""
// on any failure.
func platformUUID() string {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	m := ioregUUIDPattern.FindSubmatch(out)
	if m == nil {
		return ""
	}
	return string(m[1])
}
