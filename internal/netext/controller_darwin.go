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
)

// darwinController is the real Controller: it tracks the captured-app set and,
// when the system extension is approved, writes the singctl<->extension
// config.json into the App Group shared container. The extension picks it up
// (providerConfiguration / file watch — see packaging/macos/netextension).
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

func (c *darwinController) Targets() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.set.list()
}

// flushLocked persists the current set to config.json (caller holds c.mu). All
// failures are wrapped with the offending path so the TUI can show why per-app
// capture didn't take (e.g. the CLI isn't signed with the App Group entitlement,
// so the shared container isn't writable — see LICENSATION.md).
func (c *darwinController) flushLocked() error {
	if c.cfgPath == "" {
		return errors.New("netext: не удалось определить путь App Group config.json (HOME не задан?)")
	}
	data, err := Config{Targets: c.set.list(), SocksHost: c.socks, SocksPort: c.port}.Marshal()
	if err != nil {
		return fmt.Errorf("netext: marshal config: %w", err)
	}
	dir := filepath.Dir(c.cfgPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("netext: создать каталог конфигурации %s: %w", dir, err)
	}
	if err := os.WriteFile(c.cfgPath, data, 0o644); err != nil {
		return fmt.Errorf("netext: записать %s: %w (CLI подписан с App Group entitlement? см. LICENSATION.md)", c.cfgPath, err)
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
