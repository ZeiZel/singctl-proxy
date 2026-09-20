//go:build darwin

package sysproxy

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// New returns the real, `networksetup`-backed NetworkSetup. Constructing it
// runs nothing — every method is a one-shot `networksetup` invocation issued
// only when called (see the package doc's SAFETY note).
func New() NetworkSetup { return darwinNetworkSetup{} }

// darwinNetworkSetup shells out to `networksetup(8)`, mirroring the exact
// commands the Makefile's proxy-on/proxy-pac/proxy-off/proxy-status targets
// run. This is the one file in the package allowed to import os/exec — see
// internal/arch/imports_test.go's execAllowed (*_darwin.go).
type darwinNetworkSetup struct{}

func (darwinNetworkSetup) run(args ...string) (string, error) {
	out, err := exec.Command("networksetup", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("networksetup %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// SetAutoProxyURL mirrors `networksetup -setautoproxyurl <service> <url>`
// followed by `-setautoproxystate <service> on`.
func (d darwinNetworkSetup) SetAutoProxyURL(service, url string) error {
	if _, err := d.run("-setautoproxyurl", service, url); err != nil {
		return err
	}
	_, err := d.run("-setautoproxystate", service, "on")
	return err
}

// DisableAutoProxy mirrors `networksetup -setautoproxystate <service> off`.
func (d darwinNetworkSetup) DisableAutoProxy(service string) error {
	_, err := d.run("-setautoproxystate", service, "off")
	return err
}

// DisableWebProxy mirrors `networksetup -setwebproxystate <service> off` and
// `-setsecurewebproxystate <service> off` (proxy-off/proxy-on/proxy-pac all
// clear both HTTP and HTTPS manual proxy state together).
func (d darwinNetworkSetup) DisableWebProxy(service string) error {
	if _, err := d.run("-setwebproxystate", service, "off"); err != nil {
		return err
	}
	_, err := d.run("-setsecurewebproxystate", service, "off")
	return err
}

// SetBypassDomains mirrors `networksetup -setproxybypassdomains <service>
// <domain1> <domain2> ...`, or `... "Empty"` when domains is empty (the
// documented way to clear the list — mirrors `make proxy-off`).
func (d darwinNetworkSetup) SetBypassDomains(service string, domains []string) error {
	args := make([]string, 0, len(domains)+2)
	args = append(args, "-setproxybypassdomains", service)
	if len(domains) == 0 {
		args = append(args, "Empty")
	} else {
		args = append(args, domains...)
	}
	_, err := d.run(args...)
	return err
}

// Status mirrors `networksetup -getwebproxy`/`-getsecurewebproxy`/
// `-getautoproxyurl`/`-getproxybypassdomains <service>`, parsing their
// "Key: Value" line format (see proxy-status's target) — except
// -getproxybypassdomains, which just lists one domain per line.
func (d darwinNetworkSetup) Status(service string) (State, error) {
	var st State

	out, err := d.run("-getwebproxy", service)
	if err != nil {
		return st, err
	}
	st.WebProxyEnabled = strings.EqualFold(statusField(out, "Enabled"), "Yes")

	out, err = d.run("-getsecurewebproxy", service)
	if err != nil {
		return st, err
	}
	st.SecureWebProxyEnabled = strings.EqualFold(statusField(out, "Enabled"), "Yes")

	out, err = d.run("-getautoproxyurl", service)
	if err != nil {
		return st, err
	}
	st.AutoProxyEnabled = strings.EqualFold(statusField(out, "Enabled"), "Yes")
	st.AutoProxyURL = statusField(out, "URL")

	out, err = d.run("-getproxybypassdomains", service)
	if err != nil {
		return st, err
	}
	st.BypassDomains = parseBypassDomains(out)

	return st, nil
}

// parseBypassDomains parses `-getproxybypassdomains`'s output: one domain
// per line, or a "There aren't any..."/"Empty" line when unset.
func parseBypassDomains(out string) []string {
	var domains []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			continue
		case strings.EqualFold(line, "Empty"):
			continue
		case strings.Contains(strings.ToLower(line), "aren't any"):
			continue
		}
		domains = append(domains, line)
	}
	return domains
}

// statusField extracts the value of a "Key: Value" line from networksetup's
// output.
func statusField(out, key string) string {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		name, val, ok := strings.Cut(sc.Text(), ":")
		if ok && strings.TrimSpace(name) == key {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

// Services mirrors `networksetup -listallnetworkservices`, dropping its
// leading explanatory header line and the "*" prefix networksetup puts on
// disabled services.
func (d darwinNetworkSetup) Services() ([]string, error) {
	out, err := d.run("-listallnetworkservices")
	if err != nil {
		return nil, err
	}
	var services []string
	sc := bufio.NewScanner(strings.NewReader(out))
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first {
			first = false
			continue // "An asterisk (*) denotes that a network service is disabled."
		}
		if line == "" {
			continue
		}
		services = append(services, strings.TrimPrefix(line, "*"))
	}
	return services, nil
}

// legacyPACAgentLabel/legacyPACAgentPlistPath mirror
// packaging/macos/scripts/preinstall's PAC_LABEL and
// ~/Library/LaunchAgents/com.singctl.pacserver.plist — the standalone
// mac-proxy utility's (and singctl's own pre-2.0 `make pac-server` target's)
// LaunchAgent, whose port F1 is about.
const legacyPACAgentLabel = "com.singctl.pacserver"

func legacyPACAgentPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("sysproxy: resolve home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", legacyPACAgentLabel+".plist"), nil
}

// NewPortDiagnostics returns the real, lsof/ps/launchctl-backed
// PortDiagnostics (see the PortDiagnostics doc in portdiag.go). Constructing
// it runs nothing — every method is a one-shot invocation issued only when
// called, same as New().
func NewPortDiagnostics() PortDiagnostics { return darwinNetworkSetup{} }

// HolderOf reports the pid and executable path of whatever process is
// LISTENing on 127.0.0.1:port, via `lsof` (to find the pid) then `ps` (to
// resolve it to a path — macOS's `ps -o comm=` prints the full executable
// path, unlike Linux's truncated comm). Real singctl only ever calls this
// after a pinned PAC-server bind has already failed (see
// Manager.diagnoseBindError) — never speculatively, and never to decide
// whether to act, only to report.
func (d darwinNetworkSetup) HolderOf(port int) (PortHolder, bool, error) {
	out, err := exec.Command("lsof", "-nP", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN", "-t").CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		// lsof exits non-zero with empty output when nothing matches (the
		// common case: something else raced to grab the port and has
		// already let go, or the lookup just isn't permitted) — that's
		// "couldn't tell", not an error worth surfacing.
		if text == "" {
			return PortHolder{}, false, nil
		}
		return PortHolder{}, false, fmt.Errorf("lsof -iTCP:%d: %w: %s", port, err, text)
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return PortHolder{}, false, nil
	}
	pid, perr := strconv.Atoi(fields[0])
	if perr != nil {
		return PortHolder{}, false, fmt.Errorf("lsof -iTCP:%d: unexpected pid output %q", port, fields[0])
	}
	pathOut, perr := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).CombinedOutput()
	path := strings.TrimSpace(string(pathOut))
	if perr != nil || path == "" {
		path = "(unknown)"
	}
	return PortHolder{PID: pid, Path: path}, true, nil
}

// LegacyPACAgent reports whether singctl's own legacy PAC LaunchAgent plist
// exists on disk and, if so, whether it verifiably matches singctl's own
// shape (see IsLegacyPACAgentPlist) — reading the file only, never touching
// it.
func (d darwinNetworkSetup) LegacyPACAgent() (present, owned bool, path string, err error) {
	path, err = legacyPACAgentPlistPath()
	if err != nil {
		return false, false, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, false, path, nil
		}
		return false, false, path, fmt.Errorf("sysproxy: read %s: %w", path, err)
	}
	return true, IsLegacyPACAgentPlist(data), path, nil
}

// RemoveLegacyPACAgent boots out and deletes singctl's own legacy PAC
// LaunchAgent. It re-verifies ownership itself — defense in depth on top of
// Manager.ReclaimPort's own check — and refuses to touch anything that
// doesn't match, even if a caller skipped that check.
func (d darwinNetworkSetup) RemoveLegacyPACAgent() error {
	present, owned, path, err := d.LegacyPACAgent()
	if err != nil {
		return fmt.Errorf("sysproxy: reclaim port: %w", err)
	}
	if !present {
		return errors.New("sysproxy: reclaim port: no legacy PAC LaunchAgent found")
	}
	if !owned {
		return fmt.Errorf("sysproxy: reclaim port: %s is not singctl's own legacy PAC agent — refusing to remove it", path)
	}
	// Best-effort: bootout tolerates "not loaded" (mirrors preinstall step
	// 2); the plist removal below is what actually matters for "never binds
	// that port again".
	_, _ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), legacyPACAgentLabel)).CombinedOutput()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("sysproxy: reclaim port: remove %s: %w", path, err)
	}
	return nil
}
