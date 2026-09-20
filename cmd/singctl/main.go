// Command singctl runs a proxy on an embedded sing-box core and toggles
// a system VPN (TUN) mode, while passively coexisting with Cisco Secure
// Client (observe-only). It is driven entirely by flags: --headless/--daemon
// run the proxy (foreground or detached); --status/--attach/--stop manage a
// running instance; a bare invocation with nothing running prints status/help.
// Build the shipping binary with `-tags singbox`; the default build links a
// stub core for the hermetic tests.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/joho/godotenv"

	"singctl/internal/app"
	"singctl/internal/control"
	"singctl/internal/core"
	"singctl/internal/daemon"
	"singctl/internal/firewall"
	"singctl/internal/monitor"
	"singctl/internal/netext"
	"singctl/internal/netstate"
	"singctl/internal/notify"
	"singctl/internal/platform"
	"singctl/internal/proclist"
	"singctl/internal/procproxy"
	"singctl/internal/profile"
	"singctl/internal/protocol/all"
	"singctl/internal/remote"
	"singctl/internal/runtime"
	"singctl/internal/singbox"
	"singctl/internal/sub"
	"singctl/internal/types"
)

const pollInterval = 2 * time.Second

// version is stamped by the Makefile via -ldflags "-X main.version=...".
var version = "dev"

//go:embed singctl.1
var manPage string

// realConfigDir resolves the config dir + the uid/gid to chown files back to.
// Under sudo it uses the invoking user's ~/.config/singctl (so files aren't
// left root-owned); run directly as root (or any euid) it falls back to the
// current user's home, so the instance advertisement + control socket — and thus
// attach/--stop and the second-launch bind-crash protection — keep working.
// uid/gid are -1 when no chown-back is needed (we already are that user).
func realConfigDir() (dir string, uid, gid int) {
	if ru, err := platform.ResolveUser(os.Getenv, user.Lookup); err == nil {
		return filepath.Join(ru.HomeDir, ".config", "singctl"), ru.Uid, ru.Gid
	}
	// No SUDO_USER (plain root or a normal non-sudo run): use this process's home.
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if home = os.Getenv("HOME"); home == "" {
			return "", -1, -1
		}
	}
	return filepath.Join(home, ".config", "singctl"), -1, -1
}

// resolveLaunchUser returns the real (non-root) user that apps launched/restarted
// through the proxy should run as. singctl runs as root (sudo); GUI apps like Zen
// refuse to run as root in a user's session, so we drop launched children back to
// this user. Returns nil when we aren't root or no real user resolves (then the
// child already runs as the invoking user).
func resolveLaunchUser() *procproxy.LaunchUser {
	if os.Geteuid() != 0 {
		return nil
	}
	ru, err := platform.ResolveUser(os.Getenv, user.Lookup)
	if err != nil || ru.Uid <= 0 {
		return nil
	}
	return &procproxy.LaunchUser{Uid: ru.Uid, Gid: ru.Gid, Name: ru.Username, Home: ru.HomeDir}
}

// waitForInstance polls for a live advertised instance whose PID differs from
// notPID (the spawning parent), up to timeout. Reports whether the daemon child
// came up.
func waitForInstance(dir string, notPID int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if inst, err := control.ReadInstance(dir); err == nil && inst.PID != notPID && control.IsAlive(inst.PID) {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}

// confirm shows a huh yes/no prompt for a destructive action. It returns true
// when skip is set (--yes), or when there's no interactive terminal (so scripts
// aren't blocked — the caller is expected to have passed --yes for automation,
// but a non-TTY shouldn't hang). Otherwise it asks and returns the choice.
func confirm(title, desc string, skip bool) bool {
	if skip {
		return true
	}
	ok := false
	err := huh.NewConfirm().
		Title(title).
		Description(desc).
		Affirmative("Yes").
		Negative("Cancel").
		Value(&ok).
		Run()
	if err != nil {
		// No TTY / aborted → treat as "no" so we never act without consent.
		return false
	}
	return ok
}

// chownTo chowns a path back to the invoking user, or does nothing when there is
// no separate user to restore ownership to (uid < 0).
func chownTo(path string, uid, gid int) {
	if uid < 0 {
		return
	}
	_ = os.Chown(path, uid, gid)
}

// maxLogBytes caps singbox.log; past this it is rolled over to singbox.log.1 at
// startup (sing-box has no built-in rotation).
const maxLogBytes = 8 << 20 // 8 MiB

// rotateLogIfLarge renames path→path.1 (overwriting any old .1) when path
// exceeds cap, so the fresh log starts empty. Best-effort; ownership of the
// rolled-over file is preserved for the real user. No-op if path is small/absent.
func rotateLogIfLarge(path string, cap int64, uid, gid int) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() <= cap {
		return
	}
	rolled := path + ".1"
	if os.Rename(path, rolled) == nil {
		chownTo(rolled, uid, gid)
	}
}

// startupAction is what a normal launch should do given a possibly-running peer.
type startupAction int

const (
	actLocal          startupAction = iota // start our own cores (needs root)
	actRemoteHeadless                      // attach: apply mode/settings via socket, exit
)

// decideStartup is pure (unit-tested). The just-spawned daemon child and an
// explicit --daemon always run locally; with no live peer we start cores;
// otherwise we attach to it (apply mode/settings via the control socket, print
// status, exit — there is no interactive UI to fall back to).
func decideStartup(alive, daemonFlag, isChild bool) startupAction {
	if daemonFlag || isChild || !alive {
		return actLocal
	}
	return actRemoteHeadless
}

// runRemoteHeadless applies the requested mode to a running instance and exits.
func runRemoteHeadless(inst control.Instance, c *cli) int {
	rb := remote.New(inst, c.proxy.port, nil)
	defer rb.Close()
	ctx := context.Background()
	switch {
	case c.proxy.vpn:
		if err := rb.EnableVPN(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
	case c.proxy.proxy:
		if err := rb.EnableProxy(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
	}
	if st, err := rb.Status(); err == nil {
		fmt.Printf("singctl: instance PID %d, mode %s\n", st.PID, st.Mode)
	}
	return 0
}

// printBareStatus is what a bare `singctl` (no flags, no running instance)
// prints: there is no interactive UI to fall back to, so it reports that
// nothing is running and shows how to start it, followed by --help.
func printBareStatus(c *cli) {
	fmt.Println("singctl: no running instance.")
	fmt.Println("Start it with --headless (foreground) or --daemon (background), e.g.:")
	fmt.Println("  sudo singctl --headless --key <vless://...>")
	fmt.Println("  sudo singctl --daemon --key <vless://...>")
	fmt.Println()
	c.reg.Help(os.Stdout)
}

// launchDaemonPlist is where `make install` puts the macOS system daemon.
const launchDaemonPlist = "/Library/LaunchDaemons/com.singctl.proxy.plist"

// proxyPorts is the set of local ports the proxy listens on (SOCKS + HTTP), from
// the CLI port or the defaults.
func proxyPorts(c *cli) []int {
	socks := c.proxy.port
	if socks == 0 {
		socks = 1080
	}
	return []int{socks, socks + 1, 1080, 2080}
}

// portHolders enumerates processes listening on any of the given ports, split
// into singctl processes (safe to terminate to free the port) and foreign ones
// (reported, never killed). Best-effort; an enumeration error yields nothing.
func portHolders(ports []int) (singctlPIDs []int, foreign []proclist.App) {
	apps, err := proclist.NewLister().List(context.Background())
	if err != nil {
		return nil, nil
	}
	self := os.Getpid()
	for _, a := range apps {
		if a.PID == self || !anyPortIn(a.Ports, ports) {
			continue
		}
		if strings.Contains(strings.ToLower(a.Name), "singctl") {
			singctlPIDs = append(singctlPIDs, a.PID)
		} else {
			foreign = append(foreign, a)
		}
	}
	return singctlPIDs, foreign
}

func anyPortIn(have, want []int) bool {
	for _, h := range have {
		for _, w := range want {
			if h == w {
				return true
			}
		}
	}
	return false
}

// runControlCommand handles --attach/--stop/--status against a running instance.
// These never need root: they only read the advertisement file, the log file and
// the control socket.
func runControlCommand(c *cli) int {
	dir, _, _ := realConfigDir()
	if dir == "" {
		fmt.Fprintln(os.Stderr, "error: cannot resolve config directory")
		return 1
	}
	inst, err := control.ReadInstance(dir)
	alive := err == nil && control.IsAlive(inst.PID)
	if !alive {
		// The advertisement is missing or stale (points at a dead PID). Clean it
		// up so it stops misleading discovery, then — for --stop — try to stop a
		// system LaunchDaemon, which advertises elsewhere (it runs as root) and so
		// can't be reached over this socket.
		if err == nil {
			fmt.Fprintf(os.Stderr, "note: removing stale advertisement (PID %d not running)\n", inst.PID)
			control.RemoveInstance(dir)
		}
		if c.ctl.stop {
			freed := false
			if ok, msg := stopSystemDaemon(); ok {
				fmt.Println(msg)
				freed = true
			}
			// Also free the ports from any orphaned singctl process that no
			// advertisement tracks (e.g. a detached --daemon child), and report a
			// foreign holder.
			ports := proxyPorts(c)
			sc, foreign := portHolders(ports)
			for _, pid := range sc {
				if err := syscall.Kill(pid, syscall.SIGTERM); err == nil {
					fmt.Printf("singctl: stopped process PID %d (was holding the port)\n", pid)
					freed = true
				}
			}
			for _, a := range foreign {
				fmt.Fprintf(os.Stderr, "note: port is held by a foreign process %q (PID %d) — not singctl\n", a.Name, a.PID)
			}
			if freed {
				return 0
			}
		}
		fmt.Fprintln(os.Stderr, "error: no running singctl instance found.")
		if goruntime.GOOS == "darwin" {
			fmt.Fprintln(os.Stderr, "  If the port is held by the system daemon — reinstall it (make install),")
			fmt.Fprintln(os.Stderr, "  or stop it: sudo launchctl bootout system "+launchDaemonPlist)
		}
		return 1
	}
	switch {
	case c.ctl.stop:
		if !confirm(fmt.Sprintf("Stop singctl (PID %d)?", inst.PID),
			"The proxy will stop working.", c.root.yes) {
			fmt.Println("cancelled")
			return 0
		}
		if err := control.Stop(inst.ControlSocket); err != nil {
			fmt.Fprintln(os.Stderr, "error: stop:", err)
			return 1
		}
		fmt.Printf("singctl: asked PID %d to stop\n", inst.PID)
		return 0
	case c.ctl.status:
		st, err := control.QueryStatus(inst.ControlSocket)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: status:", err)
			return 1
		}
		fmt.Printf("singctl: PID %d, mode %s, started %s\n", st.PID, st.Mode, st.StartedAt)
		if st.CiscoActive {
			if st.ProxyBypass {
				fmt.Printf("  Cisco: active — proxy is bypassing Cisco (egress via %s)\n", st.PhysIface)
			} else {
				fmt.Println("  Cisco: active — proxy is riding Cisco (fallback: no physical interface bound)")
			}
		}
		return 0
	default: // --attach
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		fmt.Printf("singctl: attached to PID %d (mode %s) — tailing %s, ctrl+c to detach\n",
			inst.PID, inst.Mode, inst.LogPath)
		if err := tailFile(ctx, inst.LogPath); err != nil {
			fmt.Fprintln(os.Stderr, "error: attach:", err)
			return 1
		}
		return 0
	}
}

// tailFile follows a file (like `tail -f`), printing new lines to stdout until
// ctx is cancelled. It starts near the end so the user isn't flooded with old
// logs.
func tailFile(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > 4096 {
		_, _ = f.Seek(-4096, io.SeekEnd)
	}
	r := bufio.NewReader(f)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		line, err := r.ReadString('\n')
		if len(line) > 0 {
			fmt.Print(line)
		}
		if err == io.EOF {
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if err != nil {
			return err
		}
	}
}

// registerControl wires the control-socket commands a remote invocation uses to
// drive this running instance.
func registerControl(srv *control.Server, executor *app.Executor, stop func(), startedAt string) {
	srv.Handle("STATUS", func(string) (string, error) {
		cisco, bypass, phys := executor.CoexistStatus()
		data, _ := json.Marshal(control.Status{
			PID: os.Getpid(), Mode: executor.StateLabel(), StartedAt: startedAt,
			CiscoActive: cisco, ProxyBypass: bypass, PhysIface: phys,
			NetextSupported: netext.Supported, NetextAvailable: netext.Available(),
		})
		return string(data), nil
	})
	srv.Handle("STOP", func(string) (string, error) {
		go stop()
		return "OK", nil
	})
	srv.Handle("MODE", func(arg string) (string, error) {
		ctx := context.Background()
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "off":
			return "OK", executor.Stop(ctx)
		case "proxy":
			return "OK", executor.EnableProxy(ctx)
		case "vpn":
			return "OK", executor.EnableVPN(ctx)
		}
		return "", fmt.Errorf("unknown mode %q", arg)
	})
	srv.Handle("SETTINGS-GET", func(string) (string, error) {
		data, _ := json.Marshal(executor.CurrentSettings())
		return string(data), nil
	})
	srv.Handle("SETTINGS-SET", func(arg string) (string, error) {
		var s notify.Settings
		if err := json.Unmarshal([]byte(arg), &s); err != nil {
			return "", fmt.Errorf("bad settings json: %w", err)
		}
		return "OK", executor.ApplySettings(context.Background(), s)
	})
	srv.Handle("KEYS-GET", func(string) (string, error) {
		return strings.Join(executor.CurrentLinks(), "\n"), nil
	})
	srv.Handle("KEYS-ADD", func(arg string) (string, error) {
		return "OK", executor.AddLink(context.Background(), arg)
	})
	// KEYS-ADD-CONFIG carries a config-input key (a WireGuard INI file, so
	// far the only one) as base64 (StdEncoding) of the raw text. The control
	// protocol is one line per request (see control.HandlerFunc), and a
	// WireGuard config is inherently multi-line, so it cannot travel as
	// KEYS-ADD's plain argument — it would arrive truncated at the first
	// newline with no error. Base64 needs no escaping rules and cannot
	// collide with any other command's argument framing (notably
	// SETTINGS-SET's JSON), which is why this is a new command rather than
	// an escaping convention layered onto KEYS-ADD.
	srv.Handle("KEYS-ADD-CONFIG", func(arg string) (string, error) {
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(arg))
		if err != nil {
			return "", fmt.Errorf("bad base64: %w", err)
		}
		return "OK", executor.AddLink(context.Background(), string(data))
	})
	srv.Handle("KEYS-REMOVE", func(arg string) (string, error) {
		idx, err := strconv.Atoi(strings.TrimSpace(arg))
		if err != nil {
			return "", fmt.Errorf("bad index %q: %w", arg, err)
		}
		return "OK", executor.DeleteLink(context.Background(), idx)
	})
	srv.Handle("KEYS-RENAME", func(arg string) (string, error) {
		idxStr, name, _ := strings.Cut(strings.TrimSpace(arg), " ")
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			return "", fmt.Errorf("bad index %q: %w", idxStr, err)
		}
		return "OK", executor.RenameLink(context.Background(), idx, name)
	})
	// SUB-*: subscriptions. Their servers are owned by the subscription, so the
	// KEYS-* family deliberately refuses to rename or delete them individually
	// (see internal/app/subscriptions.go) — these are the commands that manage
	// them as a unit.
	srv.Handle("SUB-LIST", func(string) (string, error) {
		data, err := json.Marshal(executor.Subscriptions())
		if err != nil {
			return "", err
		}
		return string(data), nil
	})
	srv.Handle("SUB-ADD", func(arg string) (string, error) {
		return "OK", executor.AddSubscription(context.Background(), arg)
	})
	srv.Handle("SUB-REMOVE", func(arg string) (string, error) {
		return "OK", executor.RemoveSubscription(context.Background(), arg)
	})
	// PROXY-GROUP/PROXY-SELECT: manual selection within the multi-server
	// failover group (see app.ProxyGroup/SelectProxy). PROXY-GROUP is a
	// no-op (Available=false, no error) in single-server mode.
	srv.Handle("PROXY-GROUP", func(string) (string, error) {
		group, err := executor.ProxyGroup(context.Background())
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(group)
		return string(data), nil
	})
	srv.Handle("PROXY-SELECT", func(arg string) (string, error) {
		return "OK", executor.SelectProxy(context.Background(), strings.TrimSpace(arg))
	})
	// SYSPROXY-*: the macOS system-proxy (PAC) toggle — see internal/sysproxy.
	// Applying requires root (real `networksetup` calls); the daemon has it,
	// the GUI does not, which is why the GUI drives this over the control
	// socket instead of shelling out itself.
	srv.Handle("SYSPROXY-STATUS", func(string) (string, error) {
		data, err := json.Marshal(executor.SysProxyStatus())
		if err != nil {
			return "", err
		}
		return string(data), nil
	})
	// SYSPROXY-SET's JSON body is unmarshaled ONTO the currently-applied
	// config (not a zero value): the GUI's mode picker only ever sends
	// `{"mode": "..."}` (see macos/Singctl/App/Core/Models.swift's
	// SysProxySetRequest), and json.Unmarshal leaves fields absent from the
	// payload untouched — so a mode-only request preserves
	// host/port/service/Proxy/Direct/etc. from whatever's already live
	// instead of zeroing them out (which would otherwise fail Validate, or
	// silently drop the rule lists).
	srv.Handle("SYSPROXY-SET", func(arg string) (string, error) {
		cfg := executor.SysProxyConfig()
		if err := json.Unmarshal([]byte(arg), &cfg); err != nil {
			return "", fmt.Errorf("bad sysproxy config json: %w", err)
		}
		return "OK", executor.SysProxySet(cfg)
	})
	// SYSPROXY-CONFIG returns the INI rules format (sysproxy.Config.INI) —
	// see internal/sysproxy/ini.go's doc comment.
	srv.Handle("SYSPROXY-CONFIG", func(string) (string, error) {
		return string(executor.SysProxyConfigINI()), nil
	})
	// SYSPROXY-IMPORT carries an INI Config (preferred — see
	// internal/sysproxy/ini.go), a legacy YAML Config, OR a plain
	// newline-separated domain list (see sysproxy.DecodeImport) as base64
	// (StdEncoding) of the raw text — same one-line-per-request reasoning as
	// KEYS-ADD-CONFIG above.
	srv.Handle("SYSPROXY-IMPORT", func(arg string) (string, error) {
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(arg))
		if err != nil {
			return "", fmt.Errorf("bad base64: %w", err)
		}
		return "OK", executor.SysProxyImport(data)
	})
	// SYSPROXY-RECLAIM-PORT removes singctl's own legacy PAC LaunchAgent (see
	// F1 in docs/v2-spec.md) — the standalone mac-proxy utility's (and
	// singctl's own pre-2.0 `make pac-server` target's) com.singctl.pacserver
	// LaunchAgent, which can hold the exact port singctl's in-process PAC
	// server wants. Only ever runs on this explicit request, and only when
	// the on-disk plist verifiably matches singctl's own legacy shape — never
	// for a foreign process, even one occupying the same port. The GUI calls
	// this from a button surfaced after a SYSPROXY-SET/-STATUS bind error
	// names the conflict as singctl's own legacy agent.
	srv.Handle("SYSPROXY-RECLAIM-PORT", func(string) (string, error) {
		return "OK", executor.SysProxyReclaimPort()
	})
	srv.Handle("SUB-UPDATE", func(string) (string, error) {
		// Bounded independently of the caller: a wedged panel must not hold the
		// control socket open indefinitely.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		changed, err := executor.RefreshSubscriptions(ctx)
		// A partial failure is not a failed command: some panels updated, and
		// the ones that did not carry their own last_error, which SUB-LIST
		// reports per subscription. Only a refresh that achieved nothing is
		// surfaced as an error here.
		if err != nil && changed == 0 {
			return "", err
		}
		return strconv.Itoa(changed), nil
	})
	srv.Handle("CONSOLE-POLL", func(arg string) (string, error) {
		since, _ := strconv.Atoi(strings.TrimSpace(arg))
		// Compact JSON (one line): the control protocol is line-delimited, and
		// json escapes any newlines inside the captured text.
		data, err := json.Marshal(executor.ConsoleSince(since))
		if err != nil {
			return "", err
		}
		return string(data), nil
	})
	// TRAFFIC: cumulative up/down byte counters so an unprivileged GUI can chart
	// throughput by sampling deltas (the daemon owns the Clash API).
	srv.Handle("TRAFFIC", func(string) (string, error) {
		tr, err := executor.TrafficSnapshot(context.Background())
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(tr)
		return string(data), nil
	})
	// PROC-*: per-process routing executed INSIDE the root daemon, so an
	// unprivileged client (the GUI) can route/launch apps it could not touch
	// itself (Linux cgroup/nftables need root). Launched children become the
	// daemon's, and their output flows through the console ring (CONSOLE-POLL).
	srv.Handle("PROC-LIST", func(string) (string, error) {
		rows, err := executor.ListProcesses(context.Background())
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(rows)
		return string(data), nil
	})
	srv.Handle("PROC-LIST-ROUTED", func(string) (string, error) {
		data, _ := json.Marshal(executor.ListRouted())
		return string(data), nil
	})
	srv.Handle("PROC-ROUTE", func(arg string) (string, error) {
		pid, err := parsePID(arg)
		if err != nil {
			return "", err
		}
		return "OK", executor.RoutePID(context.Background(), pid)
	})
	srv.Handle("PROC-UNROUTE", func(arg string) (string, error) {
		pid, err := parsePID(arg)
		if err != nil {
			return "", err
		}
		return "OK", executor.UnroutePID(context.Background(), pid)
	})
	srv.Handle("PROC-KILL", func(arg string) (string, error) {
		pid, err := parsePID(arg)
		if err != nil {
			return "", err
		}
		return "OK", executor.StopProxied(context.Background(), pid)
	})
	srv.Handle("PROC-RESTART", func(arg string) (string, error) {
		pid, err := parsePID(arg)
		if err != nil {
			return "", err
		}
		newPID, err := executor.RestartProxied(context.Background(), pid)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(newPID), nil
	})
	srv.Handle("PROC-LAUNCH", func(arg string) (string, error) {
		var argv []string
		if err := json.Unmarshal([]byte(arg), &argv); err != nil {
			return "", fmt.Errorf("bad argv json: %w", err)
		}
		pid, err := executor.LaunchProxied(context.Background(), argv)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(pid), nil
	})
	// APP-*: whole-application routing by bundle ID (the macOS system
	// extension's capture key), so the GUI's Apps tab can route/unroute every
	// process of an app — including Electron helpers — as one unit instead of
	// one PID at a time. APP-LIST only reports apps whose bundle ID resolves
	// (macOS); APP-ROUTE/APP-UNROUTE error where the platform router doesn't
	// support it (see procproxy.BundleRouter).
	srv.Handle("APP-LIST", func(string) (string, error) {
		rows, err := executor.ListApplications(context.Background())
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(rows)
		return string(data), nil
	})
	srv.Handle("APP-LIST-ROUTED", func(string) (string, error) {
		data, _ := json.Marshal(executor.ListRoutedApps())
		return string(data), nil
	})
	srv.Handle("APP-ROUTE", func(arg string) (string, error) {
		return "OK", executor.RouteApp(context.Background(), strings.TrimSpace(arg))
	})
	srv.Handle("APP-UNROUTE", func(arg string) (string, error) {
		return "OK", executor.UnrouteApp(context.Background(), strings.TrimSpace(arg))
	})
	// APP-LIST-PROXIED/APP-LAUNCH/APP-SET-ENABLED/APP-REMOVE back the GUI's
	// persistent proxied-apps store (internal/app/appstore.go): unlike APP-LIST
	// above (a live snapshot of running processes), these survive daemon
	// restarts and don't depend on any PID being alive.
	srv.Handle("APP-LIST-PROXIED", func(string) (string, error) {
		rows, err := executor.ListProxiedApps(context.Background())
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(rows)
		return string(data), nil
	})
	srv.Handle("APP-LAUNCH", func(arg string) (string, error) {
		pid, err := executor.LaunchProxiedApp(context.Background(), arg)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(pid), nil
	})
	srv.Handle("APP-SET-ENABLED", func(arg string) (string, error) {
		var req struct {
			BundleID string `json:"bundleID"`
			Enabled  bool   `json:"enabled"`
		}
		if err := json.Unmarshal([]byte(arg), &req); err != nil {
			return "", fmt.Errorf("bad app-set-enabled json: %w", err)
		}
		return "OK", executor.SetProxiedAppEnabled(req.BundleID, req.Enabled)
	})
	srv.Handle("APP-REMOVE", func(arg string) (string, error) {
		return "OK", executor.RemoveProxiedApp(context.Background(), strings.TrimSpace(arg))
	})
	// CONNECTIONS/CONNECTION-CLOSE: the live connection table with an
	// explicit empty-state diagnosis (F6 items 1-3 in docs/v2-spec.md) and
	// per-app/per-destination aggregates (F6 item 2), backed by the Clash
	// API — see app.Executor.Connections/clashapi.Connections.
	srv.Handle("CONNECTIONS", func(string) (string, error) {
		data, err := json.Marshal(executor.Connections(context.Background()))
		if err != nil {
			return "", err
		}
		return string(data), nil
	})
	srv.Handle("CONNECTION-CLOSE", func(arg string) (string, error) {
		return "OK", executor.CloseConnection(context.Background(), strings.TrimSpace(arg))
	})
	// FIREWALL-*: the minimal firewall (F6 item 5) — block/allow a
	// destination domain/CIDR or a process, persisted and rendered into the
	// generated sing-box config's route rules (internal/firewall,
	// internal/singbox's firewallRouteRules). FIREWALL-ADD's JSON argument is
	// a firewall.Rule; FIREWALL-REMOVE's plain argument is its id.
	srv.Handle("FIREWALL-LIST", func(string) (string, error) {
		data, err := json.Marshal(executor.FirewallList())
		if err != nil {
			return "", err
		}
		return string(data), nil
	})
	srv.Handle("FIREWALL-ADD", func(arg string) (string, error) {
		var rule firewall.Rule
		if err := json.Unmarshal([]byte(arg), &rule); err != nil {
			return "", fmt.Errorf("bad firewall rule json: %w", err)
		}
		added, err := executor.FirewallAdd(context.Background(), rule)
		if err != nil {
			return "", err
		}
		data, _ := json.Marshal(added)
		return string(data), nil
	})
	srv.Handle("FIREWALL-REMOVE", func(arg string) (string, error) {
		return "OK", executor.FirewallRemove(context.Background(), strings.TrimSpace(arg))
	})
}

// parsePID parses a decimal PID argument from a control command.
func parsePID(arg string) (int, error) {
	pid, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil {
		return 0, fmt.Errorf("bad pid %q: %w", arg, err)
	}
	return pid, nil
}

// randomSecret returns a 128-bit hex token used as the default Clash API secret
// so the loopback API is not left open without authentication. On the vanishingly
// unlikely RNG failure it falls back to a fixed-but-private string.
func randomSecret() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "singctl-local"
	}
	return hex.EncodeToString(b)
}

// requireRoot enforces that we run under sudo (TUN/auto_route need privileges).
// On Windows there is no euid (os.Geteuid returns -1); elevation is checked by
// the OS itself when the TUN device is created, so the check is skipped.
func requireRoot(goos string, euid int) error {
	if goos == "windows" {
		return nil
	}
	if euid != 0 {
		return fmt.Errorf("singctl must run as root — re-run with: sudo singctl")
	}
	return nil
}

func main() {
	// The protocol registry is the composition root's one wiring point (see
	// docs/protocol-modules.md): every protocol module singctl supports is
	// listed exactly once, in internal/protocol/all.Registry, and injected
	// down from here — nothing below reaches for a package-level singleton.
	// Registry() panics only on a wiring bug (a duplicate scheme/name), never
	// on anything runtime/user input could trigger, so building it
	// unconditionally before flag parsing is safe.
	reg := all.Registry()

	c, err := parseCLI(os.Args[1:], os.Stderr, reg)
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	// Informational flags work without root.
	switch {
	case c.root.version:
		fmt.Println("singctl", version)
		return
	case c.root.man:
		fmt.Print(manPage)
		return
	}

	// Control commands target an already-running instance and need no root.
	if c.ctl.attach || c.ctl.stop || c.ctl.status {
		os.Exit(runControlCommand(c))
	}

	// .env (explicit path, or ./.env if present) feeds SINGCTL_KEY/SINGCTL_PORT;
	// real environment variables win, flags win over both.
	if c.root.envFile != "" {
		if err := godotenv.Load(c.root.envFile); err != nil {
			fmt.Fprintln(os.Stderr, "error: load env file:", err)
			os.Exit(1)
		}
	} else {
		_ = godotenv.Load() // best-effort ./.env
	}
	if err := c.applyEnv(os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	// If a live instance is already running, attach to it instead of starting our
	// own cores (which would crash on "address already in use"). This needs no root.
	var inst control.Instance
	alive := false
	if dir, _, _ := realConfigDir(); dir != "" {
		if i, err := control.ReadInstance(dir); err == nil && control.IsAlive(i.PID) {
			inst, alive = i, true
		}
	}
	if decideStartup(alive, c.proxy.daemon, daemon.IsChild()) == actRemoteHeadless {
		os.Exit(runRemoteHeadless(inst, c))
	}
	// actLocal: start our own cores (needs root) — but only if asked to actually
	// run one (--headless or --daemon; the daemon child always sets --headless,
	// see internal/daemon.BuildArgs). A bare invocation with nothing running and
	// no run flag has no interactive UI to fall back to, so it just prints
	// status-style output/help instead of starting anything.
	if !c.proxy.headless && !c.proxy.daemon {
		printBareStatus(c)
		return
	}

	if err := requireRoot(goruntime.GOOS, os.Geteuid()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// A separate cancel lets the control socket (--stop from another tab) shut us
	// down via the same path as a signal.
	ctx, cancelRun := context.WithCancel(sigCtx)
	defer cancelRun()
	startedAt := time.Now().Format("2006-01-02 15:04:05")

	// Read-only passive detector + physical-interface prober + orphan cleanup.
	detector := netstate.New(netstate.NewOSRunner())
	prober := runtime.NewNetProber(detector)
	routes := runtime.NewOSRouteController()

	notes := make(chan any, 256) // large buffer absorbs chatty Electron console output; pushNonBlocking drops on backpressure
	executor := app.NewExecutor(core.NewFactory(), reg, prober, routes, notes)
	defer executor.CloseLog()
	executor.SetSocksPort(c.proxy.port)
	executor.SetLaunchUser(resolveLaunchUser()) // drop proxied app launches to the real user (sudo)
	clashAddr := c.obs.effectiveClashAPI()
	clashSecret := c.obs.clashSecret
	if clashAddr != "" {
		if clashSecret == "" {
			clashSecret = randomSecret()
		}
		executor.SetClashAPI(clashAddr, clashSecret)
	}
	executor.SetURLTest(singbox.URLTestParams{
		URL:       c.obs.urltestURL,
		Interval:  c.obs.urltestInterval,
		Tolerance: c.obs.urltestTolerance,
	})

	// Restore the persistent per-app proxy store's enabled apps into the system
	// extension's target set now the socks port above is configured (the store
	// itself was already loaded in NewExecutor). Best-effort: an unapproved or
	// missing extension just means capture doesn't take yet, not a fatal error.
	if err := executor.RecomputeAppTargets(); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not restore the proxied-apps list:", err)
	}

	// Persist the profile + log file under the real user's home (chowned back),
	// and remember the last link. In headless --logs mode the log path stays
	// empty so sing-box writes to the console instead of the file.
	var savedLink, logPath string
	var store *profile.Store
	configDir, realUID, realGID := realConfigDir()
	if configDir != "" {
		homeDir := filepath.Dir(filepath.Dir(configDir)) // <home>/.config/singctl → <home>
		_ = os.MkdirAll(configDir, 0o755)
		chownTo(configDir, realUID, realGID)
		logPath = filepath.Join(configDir, "singbox.log")
		// Startup rotation: sing-box has no built-in rotation, so bound the file
		// by rolling it over once it exceeds the cap (1-deep: singbox.log.1).
		// Done before anyone opens it so the fresh file starts empty.
		rotateLogIfLarge(logPath, maxLogBytes, realUID, realGID)
		// Pre-create the log owned by the real user (we run as root under sudo):
		// otherwise sing-box/appendLog create it root-owned 0600 and the user
		// can't read/tail their own logs ("Permission denied").
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			_ = f.Close()
			chownTo(logPath, realUID, realGID)
		}

		store = profile.NewStore(profile.OSFS{}, homeDir, realUID, realGID)
		if !c.keys.noSave {
			executor.SetSaver(store.Save)
		}
		executor.SetIntroHook(store.MarkIntroSeen)
		if l, err := store.Load(); err == nil {
			savedLink = l
		}
		// Persisted autostart mode (F2 item 2): defaults to "off" for a fresh
		// install or one from before F2 — the LaunchDaemon's plist no longer
		// hardcodes --vpn (F2 item 1), so this is the only thing that decides
		// what runHeadless enables when no --proxy/--vpn flag is given.
		executor.SetAutostartSaver(store.SaveAutostartMode)
		if am, err := store.LoadAutostartMode(); err == nil {
			_ = executor.SetAutostartMode(am)
		}

		// Persisted system-proxy (PAC) config (F1b in docs/v2-spec.md): without
		// this, a daemon restart leaves macOS pointed at a PAC URL from the
		// previous run (dead now that pac_port defaults to an OS-assigned
		// ephemeral port — see F1). Best-effort, same rule as the autostart mode
		// above (F2 item 3): a load/parse/apply failure is only ever a warning —
		// it must never stop the daemon from starting.
		executor.SetSysProxySaver(store.SaveSysproxyConfig)
		if data, err := store.LoadSysproxyConfig(); err != nil {
			fmt.Fprintln(os.Stderr, "warning: load persisted system-proxy config:", err)
		} else if err := executor.RestoreSysProxyConfig(data); err != nil {
			fmt.Fprintln(os.Stderr, "warning: restore system-proxy config:", err)
		}

		// Persisted firewall rules (F6 item 5): rendered into the generated
		// proxy config's route rules on every load/reload (see
		// app.firewallConfigBuilder), same "load the cache now, no network/mode
		// work yet" discipline as the subscription restore right below.
		executor.SetFirewallSaver(store.SaveFirewallRules)
		if rules, err := store.LoadFirewallRules(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		} else {
			executor.RestoreFirewallRules(rules)
		}

		// Subscriptions. The cached server lists are restored WITHOUT fetching so
		// the daemon comes up with the servers it had last time even when the
		// panel is unreachable; the refresher brings them up to date shortly
		// after. The fetcher falls back to our own local HTTP proxy when a
		// direct request fails, because subscription hosts are exactly the kind
		// of host this network blocks.
		socksPort := c.proxy.port
		if socksPort == 0 {
			socksPort = 1080
		}
		executor.SetSubscriptionDeps(
			sub.NewHTTPFetcher(nil, fmt.Sprintf("127.0.0.1:%d", socksPort+1)),
			store.SaveSubscriptions,
		)
		if subs, err := store.LoadSubscriptions(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v\n", err)
		} else {
			executor.RestoreSubscriptions(subs)
		}
	} else {
		fmt.Fprintln(os.Stderr, "warning: cannot resolve a config directory — attach/--stop and instance discovery are disabled")
	}
	if !(c.proxy.headless && c.proxy.logs) {
		executor.SetLogPath(logPath)
	}
	if c.proxy.verbose {
		executor.SetLogLevel("info") // opt back into verbose per-connection logging
	}

	// --sub URLs are registered (and fetched once) before any mode starts. A
	// failure here is a warning, not a fatal: the run may still be viable on
	// manual keys or on previously cached subscription servers, and the
	// "nothing to run" case is reported by runHeadless with a better message.
	for _, u := range c.keys.subscriptions() {
		known := false
		for _, existing := range executor.Subscriptions() {
			if existing.URL == u {
				known = true
				break
			}
		}
		if known {
			continue
		}
		addCtx, cancelAdd := context.WithTimeout(context.Background(), 30*time.Second)
		if err := executor.AddSubscription(addCtx, u); err != nil {
			fmt.Fprintf(os.Stderr, "warning: subscription %s: %v\n", u, err)
		}
		cancelAdd()
	}

	// flag/env key overrides the saved profile.
	initialLink := strings.TrimSpace(c.keys.combined())
	if initialLink == "" {
		initialLink = savedLink
	}
	mode := "proxy"
	if c.proxy.vpn {
		mode = "vpn"
	}

	// --daemon (parent): persist the key, re-exec a detached headless child, exit.
	// Runs BEFORE advertising so the exiting parent never clobbers the child's
	// instance.json / control socket. The child (SINGCTL_DAEMON_CHILD=1) skips this.
	if c.proxy.daemon && !daemon.IsChild() {
		if initialLink == "" && len(executor.Subscriptions()) == 0 {
			fmt.Fprintln(os.Stderr, "error: --daemon needs a key or a subscription (--key/--sub, $SINGCTL_KEY/$SINGCTL_SUB, or a saved profile)")
			os.Exit(1)
		}
		if store != nil {
			_ = store.Save(initialLink) // the child loads the key from the profile
		}
		if configDir != "" {
			if inst, err := control.ReadInstance(configDir); err == nil && control.IsAlive(inst.PID) {
				fmt.Fprintf(os.Stderr, "error: a singctl instance is already running (PID %d) — stop it first: singctl --stop\n", inst.PID)
				os.Exit(1)
			}
		}
		if err := daemon.Spawn(daemon.Config{
			Mode: mode, Port: c.proxy.port, NoClash: c.obs.noClash,
			ClashAddr: c.obs.clashAPI, ClashSecret: clashSecret,
			URLTestURL: c.obs.urltestURL, URLTestInterval: c.obs.urltestInterval,
			URLTestTolerance: c.obs.urltestTolerance, LogPath: logPath,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "error: start daemon:", err)
			os.Exit(1)
		}
		// Wait for the child to actually come up (it advertises instance.json once
		// its cores are running). If it never appears, report failure instead of a
		// misleading success — the child likely failed to bind or load the key.
		if configDir != "" && !waitForInstance(configDir, os.Getpid(), 5*time.Second) {
			fmt.Fprintln(os.Stderr, "error: daemon did not come up — check the log:", logPath)
			os.Exit(1)
		}
		fmt.Println("singctl: daemon started in the background — manage it with --status / --attach / --stop")
		return
	}

	// Advertise this instance + serve control (attach/stop/status) so another
	// tab can follow logs and stop it. Best-effort: failures don't block startup.
	if configDir != "" {
		// Guard against a second instance stomping a live one. control.Server.Start
		// unconditionally unlinks the socket, so without this a second launch would
		// silently steal it and both would fight over ports/routes. Refuse if a
		// live instance already owns the advertisement; drop a stale one (dead PID)
		// so a fresh start can proceed cleanly. Mirrors the --daemon parent guard.
		if inst, err := control.ReadInstance(configDir); err == nil {
			if inst.PID != os.Getpid() && control.IsAlive(inst.PID) {
				fmt.Fprintf(os.Stderr, "error: singctl already running (PID %d) — stop it first: singctl --stop\n", inst.PID)
				os.Exit(1)
			}
			control.RemoveInstance(configDir) // stale advertisement from a dead PID
		}
		sockPath := filepath.Join(configDir, "control.sock")
		srv := control.NewServer(sockPath)
		registerControl(srv, executor, cancelRun, startedAt)
		if err := srv.Start(); err == nil {
			chownTo(sockPath, realUID, realGID)
			defer srv.Close()
			inst := control.Instance{
				PID: os.Getpid(), Mode: mode, LogPath: logPath, ControlSocket: sockPath,
				ClashAPIAddr: clashAddr, ClashSecret: clashSecret, StartedAt: startedAt,
			}
			_ = control.WriteInstance(configDir, inst)
			chownTo(control.InstancePath(configDir), realUID, realGID)
			defer control.RemoveInstance(configDir)
		} else {
			fmt.Fprintln(os.Stderr, "warning: control socket unavailable:", err)
		}
	}

	// Monitor: event-driven (PF_ROUTE) + 2s polling fallback, debounce 2.
	monOut := make(chan monitor.Event, 16)
	mon := monitor.New(detector, 2, executor.Mode, executor.ProxyBoundToPhysical, monOut)
	mon.SetOnPoll(func(ns types.NetState) { executor.PushDisplay(ctx, ns) })

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	events := netstate.WatchRouteChanges(ctx)

	go mon.Run(ctx, ticker.C, events)
	go executor.Loop(ctx, monOut)

	// Reaching here always means --headless is set: the daemon child always
	// passes it (internal/daemon.BuildArgs), and the bare/no-flag case already
	// returned above via printBareStatus.
	if err := runHeadless(ctx, executor, notes, initialLink, c); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// requestedMode reports the mode explicitly requested on the command line
// (--proxy/--vpn; parseCLI already rejects both together). explicit is false
// when neither flag was given — the case for the installed LaunchDaemon since
// F2 item 1 removed --vpn from its plist, and for the daemon's own re-exec of
// itself in --daemon mode with neither flag either. runHeadless falls back to
// the persisted autostart-mode setting in that case (F2 item 2).
func requestedMode(c *cli) (mode string, explicit bool) {
	switch {
	case c.proxy.vpn:
		return "vpn", true
	case c.proxy.proxy:
		return "proxy", true
	default:
		return "", false
	}
}

// reportModeResult prints the daemon's usual startup line on success; on
// failure it reports the error and explicitly says the daemon is staying up
// in "off" — it must NEVER be fatal (F2 item 3): that combination, together
// with the installed LaunchDaemon's KeepAlive, is exactly what turned "VPN
// fails to start" into an infinite restart loop.
func reportModeResult(mode string, socksPort int, err error) {
	if err != nil {
		if strings.Contains(err.Error(), "address already in use") {
			fmt.Fprintf(os.Stderr, "error: enable %s: %v — another instance is already running; use --attach/--status/--stop. Staying up in off mode.\n", strings.ToLower(mode), err)
			return
		}
		fmt.Fprintf(os.Stderr, "error: enable %s: %v — staying up in off mode; retry via MODE or the GUI\n", strings.ToLower(mode), err)
		return
	}
	socks := socksPort
	if socks == 0 {
		socks = 1080
	}
	fmt.Printf("singctl %s: %s mode up — socks 127.0.0.1:%d, http 127.0.0.1:%d (ctrl+c to stop)\n",
		version, mode, socks, socks+1)
}

// runHeadless drives the executor without a TUI: load the link, enable the
// requested (or persisted autostart) mode, print status notes to stdout and
// run until SIGINT/SIGTERM. The notes channel must be drained here — the
// executor blocks pushing into it otherwise.
//
// Per F2 (items 1-3), nothing here is fatal: a fresh install with no key yet,
// a stale/corrupt saved link, and a mode that fails to start (the reported
// "no physical interface detected" VPN failure) all leave the process running
// — listening on the control socket for KEYS-ADD/MODE/SETTINGS-SET — rather
// than exiting, which combined with the LaunchDaemon's KeepAlive is what
// produced the restart loop this replaces.
func runHeadless(ctx context.Context, executor *app.Executor, notes <-chan any, link string, c *cli) error {
	switch {
	case link != "":
		if err := executor.LoadLink(ctx, link); err != nil {
			fmt.Fprintf(os.Stderr, "warning: load key: %v — starting with no key loaded; add one via KEYS-ADD or the GUI\n", err)
		}
	case len(executor.Subscriptions()) > 0:
		// No manual keys, but subscriptions were restored from disk: run off
		// their cached servers and let the refresher bring them up to date.
		if err := executor.Reload(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "warning: load subscription servers: %v — starting with no key loaded\n", err)
		}
	default:
		fmt.Println("singctl: no key or subscription configured yet — waiting (add one via the GUI, --attach, or KEYS-ADD)")
	}

	if mode, explicit := requestedMode(c); explicit {
		enable := executor.EnableProxy
		label := "PROXY"
		if mode == "vpn" {
			enable, label = executor.EnableVPN, "VPN"
		}
		reportModeResult(label, c.proxy.port, enable(ctx))
	} else if am := executor.AutostartMode(); am != "off" {
		// The common case since F2 item 1: the LaunchDaemon starts with no
		// mode at all, and this is what brings it up — best-effort, see
		// Executor.ApplyAutostart's doc comment for why a failure here can
		// never be allowed to exit the process.
		reportModeResult(strings.ToUpper(am), c.proxy.port, executor.ApplyAutostart(ctx))
	} else {
		fmt.Println("singctl: starting in off mode (autostart is off) — enable proxy/vpn via the GUI, --attach, or MODE")
	}
	executor.StartSubscriptionRefresher(ctx)

	// Route requested PIDs and/or launch a proxied command (best-effort; errors
	// are reported but do not abort the running proxy).
	pids, _ := c.proc.routePIDs()
	for _, pid := range pids {
		if err := executor.RoutePID(ctx, pid); err != nil {
			fmt.Fprintf(os.Stderr, "route-pid %d: %v\n", pid, err)
		} else {
			fmt.Printf("singctl: routing PID %d through the proxy\n", pid)
		}
	}
	restartPIDs, _ := c.proc.restartPIDs()
	for _, pid := range restartPIDs {
		if !confirm(fmt.Sprintf("Restart PID %d in proxy mode?", pid),
			"The process will be terminated and relaunched with the proxy environment.", c.root.yes) {
			fmt.Printf("restart-pid %d: cancelled\n", pid)
			continue
		}
		if newPID, err := executor.RestartProxied(ctx, pid); err != nil {
			fmt.Fprintf(os.Stderr, "restart-pid %d: %v\n", pid, err)
		} else {
			fmt.Printf("singctl: restarted PID %d in proxy mode (new PID %d)\n", pid, newPID)
		}
	}
	if c.proc.launch {
		if pid, err := executor.LaunchProxied(ctx, c.proc.launchArgv); err != nil {
			fmt.Fprintf(os.Stderr, "launch: %v\n", err)
		} else {
			fmt.Printf("singctl: launched PID %d through the proxy\n", pid)
		}
	}

	// Drain executor/monitor notes; surface mode changes and notices.
	for {
		select {
		case <-ctx.Done():
			fmt.Println("singctl: shutting down")
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return executor.Shutdown(shutCtx)
		case msg := <-notes:
			if st, ok := msg.(notify.StatusMsg); ok && st.Note != "" {
				fmt.Printf("singctl: [%s] %s\n", st.Mode, st.Note)
			}
		}
	}
}
