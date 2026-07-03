//go:build darwin

package bridge

// This file is the bridge package's OS-exec adapter for the installed-apps
// picker: it shells out to `defaults read` to resolve each .app bundle's name
// and bundle ID (mirrors internal/netext.BundleID's style; see BundleID/
// BundleInfoPlistPath, reused here). Named *_darwin.go so it's an approved
// os/exec adapter per internal/arch's import guard (TestNoForbiddenImports),
// same as the platform's other real OS adapters.

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"singctl/internal/netext"
)

// installedAppDirs are the standard macOS locations .app bundles live in:
// system apps, user-installed apps, and the current user's own Applications
// folder (present or not — a missing dir is simply skipped).
func installedAppDirs() []string {
	dirs := []string{"/Applications", "/System/Applications"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	return dirs
}

// listInstalledApps enumerates every .app bundle under the standard
// directories for the GUI's "add an app" picker. No root needed — it only
// reads directory listings and Info.plist values, never the control socket.
// Entries without a resolvable bundle ID are skipped (RouteApp/LaunchAppBundle
// need one), and bundle IDs are deduped in case the same app shows up under
// more than one searched directory.
func listInstalledApps() ([]InstalledApp, error) {
	seen := map[string]bool{}
	out := make([]InstalledApp, 0, 64)
	for _, dir := range installedAppDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // e.g. no ~/Applications for this user — not an error
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".app") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			id := netext.BundleID(path)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			name := plistString(path, "CFBundleName")
			if name == "" {
				name = plistString(path, "CFBundleDisplayName")
			}
			if name == "" {
				name = strings.TrimSuffix(e.Name(), ".app")
			}
			out = append(out, InstalledApp{Name: name, BundleID: id, Path: path})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// plistString reads one string key from appPath's Info.plist via
// `defaults read` (works for both XML and binary plists). Returns "" on any
// error — missing key, malformed bundle, etc. — rather than failing the scan.
func plistString(appPath, key string) string {
	plist := netext.BundleInfoPlistPath(appPath)
	if plist == "" {
		return ""
	}
	base := strings.TrimSuffix(plist, ".plist") // `defaults read` wants no extension
	out, err := exec.Command("defaults", "read", base, key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
