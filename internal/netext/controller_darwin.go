//go:build darwin

package netext

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Supported is true on darwin: the system extension is a real (if possibly
// unapproved) mechanism here, vs. entirely inapplicable elsewhere.
const Supported = true

// availTTL bounds how often Available shells out to systemextensionsctl: the
// approval state changes only when the user visits System Settings, so a short
// cache avoids re-probing on every status poll (control STATUS + TUI monitor
// tick both call it, one process-wide cache serves both).
const availTTL = 5 * time.Second

var (
	availMu    sync.Mutex
	availAt    time.Time
	availCache bool
)

// Available is the free-function form of darwinController.Available: probes
// (with a short cache) whether the extension is installed AND approved.
func Available() bool {
	availMu.Lock()
	defer availMu.Unlock()
	if time.Since(availAt) < availTTL {
		return availCache
	}
	availCache = probeExtension()
	availAt = time.Now()
	return availCache
}

func probeExtension() bool {
	out, err := exec.Command("systemextensionsctl", "list").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, ExtensionID) && strings.Contains(line, "activated enabled") {
			return true
		}
	}
	return false
}

// darwinController is the real Controller: it tracks the captured-app set and,
// when the system extension is approved, writes the singctl<->extension
// config.json into the App Group shared container. The extension picks it up
// (providerConfiguration / file watch — see macos/Singctl/ProxyExtension).
type darwinController struct {
	mu      sync.Mutex
	set     *targetSet
	socks   string
	port    int
	cfgPath string // App Group shared container config.json ("" if unresolved)
}

// New returns the macOS transparent-proxy controller pinned to the local SOCKS
// proxy singctl runs. It does not require the extension to be present; mutations
// only take effect (write config.json) while Available() is true.
func New(socksHost string, socksPort int) Controller {
	return &darwinController{
		set:     newTargetSet(),
		socks:   socksHost,
		port:    socksPort,
		cfgPath: sharedConfigPath(),
	}
}

// Available best-effort probes whether the extension is installed AND approved.
// `systemextensionsctl list` shows each extension's state in brackets, e.g.
// "[activated enabled]" once the user approved it in System Settings.
func (c *darwinController) Available() bool {
	return Available()
}

func (c *darwinController) AddTarget(bundleID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.set.add(bundleID) {
		return c.flushLocked()
	}
	return nil
}

func (c *darwinController) RemoveTarget(bundleID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.set.remove(bundleID) {
		return c.flushLocked()
	}
	return nil
}

// SetTargets replaces the whole captured set wholesale and always flushes, so
// it also persists a shrink to empty (see Controller.SetTargets).
func (c *darwinController) SetTargets(bundleIDs []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.set.replace(bundleIDs)
	return c.flushLocked()
}

func (c *darwinController) Targets() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.set.list()
}

// flushLocked persists the current set to config.json (caller holds c.mu). All
// failures are wrapped with the offending path so the TUI can show why per-app
// capture didn't take (e.g. the CLI isn't signed with the App Group entitlement,
// so the shared container isn't writable.
func (c *darwinController) flushLocked() error {
	if c.cfgPath == "" {
		return errors.New("netext: could not determine App Group config.json path (HOME not set?)")
	}
	data, err := Config{Targets: c.set.list(), SocksHost: c.socks, SocksPort: c.port}.Marshal()
	if err != nil {
		return fmt.Errorf("netext: marshal config: %w", err)
	}
	dir := filepath.Dir(c.cfgPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("netext: create config directory %s: %w", dir, err)
	}
	if err := os.WriteFile(c.cfgPath, data, 0o644); err != nil {
		return fmt.Errorf("netext: write %s: %w (is the CLI signed with the App Group entitlement?)", c.cfgPath, err)
	}
	return nil
}

// sharedConfigPath resolves ~/Library/Group Containers/<AppGroup>/config.json.
func sharedConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, "Library", "Group Containers", AppGroup, "config.json")
}

// BundleID returns the CFBundleIdentifier of the .app bundle that path points at
// (or into), or "" if it can't be determined. Shells out to `defaults read`
// (mirrors the codebase's ps/lsof/nft style); the pure path walk is in
// BundleInfoPlistPath.
func BundleID(path string) string {
	plist := BundleInfoPlistPath(path)
	if plist == "" {
		return ""
	}
	// `defaults read` wants the path WITHOUT the .plist extension.
	base := strings.TrimSuffix(plist, ".plist")
	out, err := exec.Command("defaults", "read", base, "CFBundleIdentifier").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// BundleIDForPID resolves a running PID's executable path (via ps) and returns
// its bundle ID, or "" if it isn't part of an .app bundle.
func BundleIDForPID(pid int) string {
	if pid <= 0 {
		return ""
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return BundleID(strings.TrimSpace(string(out)))
}
