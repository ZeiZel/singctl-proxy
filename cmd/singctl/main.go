// Command singctl is a terminal UI that runs a VLESS proxy on an embedded
// sing-box core and toggles a system VPN (TUN) mode, while passively coexisting
// with Cisco Secure Client (observe-only). Build the shipping binary with
// `-tags singbox`; the default build links a stub core for the hermetic tests.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	_ "embed"
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
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/joho/godotenv"

	"singctl/internal/app"
	"singctl/internal/control"
	"singctl/internal/core"
	"singctl/internal/daemon"
	"singctl/internal/monitor"
	"singctl/internal/netstate"
	"singctl/internal/platform"
	"singctl/internal/procproxy"
	"singctl/internal/profile"
	"singctl/internal/remote"
	"singctl/internal/runtime"
	"singctl/internal/singbox"
	"singctl/internal/types"
	"singctl/internal/ui"
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
		Affirmative("Да").
		Negative("Отмена").
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

// startupAction is what a normal launch should do given a possibly-running peer.
type startupAction int

const (
	actLocal          startupAction = iota // start our own cores (needs root)
	actRemoteTUI                           // attach: TUI driving the running instance
	actRemoteHeadless                      // attach: apply mode/settings via socket, exit
)

// decideStartup is pure (unit-tested). The just-spawned daemon child and an
// explicit --daemon always run locally; with no live peer we start cores;
// otherwise we attach (headless → one-shot, else TUI).
func decideStartup(alive, headless, daemonFlag, isChild bool) startupAction {
	if daemonFlag || isChild || !alive {
		return actLocal
	}
	if headless {
		return actRemoteHeadless
	}
	return actRemoteTUI
}

// runProgram runs a Bubble Tea program with a panic guard so an unexpected panic
// in the reducer/render loop never crashes singctl with a raw stack dump over a
// corrupted terminal. Bubble Tea already restores the terminal on panic; this
// turns the panic into a clean error + a diagnostic on stderr. recover() is
// permitted here (the composition root) — never in Update/View.
func runProgram(p *tea.Program) (err error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintln(os.Stderr, "singctl: восстановление после паники:", r)
			os.Stderr.Write(debug.Stack())
			err = fmt.Errorf("внутренняя ошибка: %v", r)
		}
	}()
	_, err = p.Run()
	return err
}

// runRemoteTUI attaches the TUI to a running instance over its control socket.
func runRemoteTUI(inst control.Instance, c *cli) int {
	notes := make(chan tea.Msg, 256) // large buffer absorbs chatty Electron console output; pushNonBlocking drops on backpressure
	rb := remote.New(inst, c.proxy.port, notes)
	rb.SetLaunchUser(resolveLaunchUser()) // if the attach client runs under sudo, drop launches to the real user
	defer rb.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	model := ui.New(rb, notes).WithLogPath(inst.LogPath).WithAttached(inst.PID).WithLoadedProfile()
	if seed, err := rb.Settings(); err == nil {
		model = model.WithSettings(seed)
	}
	if st, err := rb.Status(); err == nil {
		model = model.WithDisplayMode(modeFromLabel(st.Mode))
	}
	model = model.WithCurrentLinks(rb.CurrentLinks())
	model = model.WithIntro(false) // short component loader when attaching

	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))
	go func() { <-ctx.Done(); program.Quit() }()
	if err := runProgram(program); err != nil {
		fmt.Fprintln(os.Stderr, "ui error:", err)
		return 1
	}
	return 0
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
		fmt.Printf("singctl: инстанс PID %d, режим %s\n", st.PID, st.Mode)
	}
	return 0
}

func modeFromLabel(s string) ui.RunMode {
	switch s {
	case "vpn":
		return ui.RunVPN
	case "proxy", "suspended":
		return ui.RunProxy
	default:
		return ui.RunOff
	}
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
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: no running singctl instance found (start one with --headless in another tab)")
		return 1
	}
	if !control.IsAlive(inst.PID) {
		fmt.Fprintf(os.Stderr, "error: instance PID %d is not running (stale advertisement)\n", inst.PID)
		return 1
	}
	switch {
	case c.ctl.stop:
		if !confirm(fmt.Sprintf("Остановить singctl (PID %d)?", inst.PID),
			"Прокси перестанет работать.", c.root.yes) {
			fmt.Println("отменено")
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
		data, _ := json.Marshal(control.Status{PID: os.Getpid(), Mode: executor.StateLabel(), StartedAt: startedAt})
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
		var s ui.Settings
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
	c, err := parseCLI(os.Args[1:], os.Stderr)
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
	switch decideStartup(alive, c.proxy.headless, c.proxy.daemon, daemon.IsChild()) {
	case actRemoteTUI:
		os.Exit(runRemoteTUI(inst, c))
	case actRemoteHeadless:
		os.Exit(runRemoteHeadless(inst, c))
	}
	// actLocal: start our own cores (needs root).

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

	notes := make(chan tea.Msg, 256) // large buffer absorbs chatty Electron console output; pushNonBlocking drops on backpressure
	executor := app.NewExecutor(core.NewFactory(), prober, routes, notes)
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

		store = profile.NewStore(profile.OSFS{}, homeDir, realUID, realGID)
		if !c.keys.noSave {
			executor.SetSaver(store.Save)
		}
		executor.SetIntroHook(store.MarkIntroSeen)
		if l, err := store.Load(); err == nil {
			savedLink = l
		}
	} else {
		fmt.Fprintln(os.Stderr, "warning: cannot resolve a config directory — attach/--stop and instance discovery are disabled")
	}
	if !(c.proxy.headless && c.proxy.logs) {
		executor.SetLogPath(logPath)
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
		if initialLink == "" {
			fmt.Fprintln(os.Stderr, "error: --daemon needs a key (--key, $SINGCTL_KEY, or a saved profile)")
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
		fmt.Println("singctl: daemon запущен в фоне — управление через --status / --attach / --stop")
		return
	}

	// Advertise this instance + serve control (attach/stop/status) so another
	// tab can follow logs and stop it. Best-effort: failures don't block startup.
	if configDir != "" {
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
	mon := monitor.New(detector, 2, executor.Mode, monOut)
	mon.SetOnPoll(func(ns types.NetState) { executor.PushDisplay(ctx, ns) })

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	events := netstate.WatchRouteChanges(ctx)

	go mon.Run(ctx, ticker.C, events)
	go executor.Loop(ctx, monOut)

	if c.proxy.headless {
		if err := runHeadless(ctx, executor, notes, initialLink, c); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	model := ui.New(executor, notes).WithLogPath(logPath).WithSettings(ui.Settings{
		SocksPort:        c.proxy.port,
		ClashEnabled:     clashAddr != "",
		ClashAddr:        c.obs.clashAPI,
		URLTestURL:       c.obs.urltestURL,
		URLTestInterval:  c.obs.urltestInterval,
		URLTestTolerance: c.obs.urltestTolerance,
		SaveProfile:      !c.keys.noSave,
	})
	if initialLink != "" {
		// Remember the link: load it (no mode started — user picks PROXY/VPN) and
		// skip the input screen.
		if err := executor.LoadLink(ctx, initialLink); err == nil {
			model = model.WithLoadedProfile().WithCurrentLink(initialLink).
				WithCurrentLinks(executor.CurrentLinks())
			switch {
			case c.proxy.proxy:
				model = model.WithAutoMode(ui.RunProxy)
			case c.proxy.vpn:
				model = model.WithAutoMode(ui.RunVPN)
			}
			if c.proxy.logs {
				model = model.WithLogsOpen()
			}
		}
	}
	// Entry animation: a full reveal on the first run (then a marker is written),
	// a short component loader on later runs. Applied last so it captures the
	// resolved screen as its post-intro target.
	firstRun := store == nil || !store.HasSeenIntro()
	model = model.WithIntro(firstRun)

	// WithAltScreen clears the terminal (alternate buffer) so earlier commands
	// aren't visible above the UI.
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))
	go func() {
		<-ctx.Done()
		program.Quit()
	}()

	if err := runProgram(program); err != nil {
		fmt.Fprintln(os.Stderr, "ui error:", err)
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = executor.Shutdown(shutCtx)
}

// runHeadless drives the executor without a TUI: load the link, enable the
// requested mode (proxy unless --vpn), print status notes to stdout and run
// until SIGINT/SIGTERM. The notes channel must be drained here — the executor
// blocks pushing into it otherwise.
func runHeadless(ctx context.Context, executor *app.Executor, notes <-chan tea.Msg, link string, c *cli) error {
	if link == "" {
		return fmt.Errorf("headless mode needs a key: pass --key, set %s (or .env), or save a profile first", envKey)
	}
	if err := executor.LoadLink(ctx, link); err != nil {
		return fmt.Errorf("load key: %w", err)
	}

	mode := "PROXY"
	enable := executor.EnableProxy
	if c.proxy.vpn {
		mode, enable = "VPN", executor.EnableVPN
	}
	if err := enable(ctx); err != nil {
		if strings.Contains(err.Error(), "address already in use") {
			return fmt.Errorf("enable %s: %w — другой инстанс уже запущен; используйте --attach/--status/--stop", strings.ToLower(mode), err)
		}
		return fmt.Errorf("enable %s: %w", strings.ToLower(mode), err)
	}
	socks := c.proxy.port
	if socks == 0 {
		socks = 1080
	}
	fmt.Printf("singctl %s: %s mode up — socks 127.0.0.1:%d, http 127.0.0.1:%d (ctrl+c to stop)\n",
		version, mode, socks, socks+1)

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
		if !confirm(fmt.Sprintf("Перезапустить PID %d в proxy-режиме?", pid),
			"Процесс будет завершён и запущен заново с прокси-окружением.", c.root.yes) {
			fmt.Printf("restart-pid %d: отменено\n", pid)
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
			if st, ok := msg.(ui.StatusMsg); ok && st.Note != "" {
				fmt.Printf("singctl: [%s] %s\n", st.Mode, st.Note)
			}
		}
	}
}
