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
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joho/godotenv"

	"singctl/internal/app"
	"singctl/internal/control"
	"singctl/internal/core"
	"singctl/internal/monitor"
	"singctl/internal/netstate"
	"singctl/internal/platform"
	"singctl/internal/profile"
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

// realConfigDir resolves the real user's ~/.config/singctl (works without root,
// resolving SUDO_USER). Returns "" if it cannot be determined.
func realConfigDir() (dir string, uid, gid int) {
	ru, err := platform.ResolveUser(os.Getenv, user.Lookup)
	if err != nil {
		return "", 0, 0
	}
	return filepath.Join(ru.HomeDir, ".config", "singctl"), ru.Uid, ru.Gid
}

// runControlCommand handles --attach/--stop/--status against a running instance.
// These never need root: they only read the advertisement file, the log file and
// the control socket.
func runControlCommand(opts *options) int {
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
	case opts.stop:
		if err := control.Stop(inst.ControlSocket); err != nil {
			fmt.Fprintln(os.Stderr, "error: stop:", err)
			return 1
		}
		fmt.Printf("singctl: asked PID %d to stop\n", inst.PID)
		return 0
	case opts.status:
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
	opts, err := parseOptions(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	// Informational flags work without root.
	switch {
	case opts.version:
		fmt.Println("singctl", version)
		return
	case opts.man:
		fmt.Print(manPage)
		return
	}

	// Control commands target an already-running instance and need no root.
	if opts.attach || opts.stop || opts.status {
		os.Exit(runControlCommand(opts))
	}

	// .env (explicit path, or ./.env if present) feeds SINGCTL_KEY/SINGCTL_PORT;
	// real environment variables win, flags win over both.
	if opts.envFile != "" {
		if err := godotenv.Load(opts.envFile); err != nil {
			fmt.Fprintln(os.Stderr, "error: load env file:", err)
			os.Exit(1)
		}
	} else {
		_ = godotenv.Load() // best-effort ./.env
	}
	if err := opts.applyEnv(os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
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

	notes := make(chan tea.Msg, 32)
	executor := app.NewExecutor(core.NewFactory(), prober, routes, notes)
	executor.SetSocksPort(opts.port)
	clashAddr := opts.effectiveClashAPI()
	clashSecret := opts.clashSecret
	if clashAddr != "" {
		if clashSecret == "" {
			clashSecret = randomSecret()
		}
		executor.SetClashAPI(clashAddr, clashSecret)
	}
	executor.SetURLTest(singbox.URLTestParams{
		URL:       opts.urltestURL,
		Interval:  opts.urltestInterval,
		Tolerance: opts.urltestTolerance,
	})

	// Persist the profile + log file under the real user's home (chowned back),
	// and remember the last link. In headless --logs mode the log path stays
	// empty so sing-box writes to the console instead of the file.
	var savedLink, logPath, configDir string
	var realUID, realGID int
	if ru, err := platform.ResolveUser(os.Getenv, user.Lookup); err == nil {
		configDir = filepath.Join(ru.HomeDir, ".config", "singctl")
		realUID, realGID = ru.Uid, ru.Gid
		_ = os.MkdirAll(configDir, 0o755)
		_ = os.Chown(configDir, ru.Uid, ru.Gid)
		logPath = filepath.Join(configDir, "singbox.log")

		store := profile.NewStore(profile.OSFS{}, ru.HomeDir, ru.Uid, ru.Gid)
		if !opts.noSave {
			executor.SetSaver(store.Save)
		}
		if l, err := store.Load(); err == nil {
			savedLink = l
		}
	}
	if !(opts.headless && opts.logs) {
		executor.SetLogPath(logPath)
	}

	// Advertise this instance + serve control (attach/stop/status) so another
	// tab can follow logs and stop it. Best-effort: failures don't block startup.
	if configDir != "" {
		mode := "proxy"
		if opts.vpn {
			mode = "vpn"
		}
		sockPath := filepath.Join(configDir, "control.sock")
		srv := control.NewServer(sockPath,
			func() control.Status {
				return control.Status{PID: os.Getpid(), Mode: executor.StateLabel(), StartedAt: startedAt}
			},
			cancelRun,
		)
		if err := srv.Start(); err == nil {
			_ = os.Chown(sockPath, realUID, realGID)
			defer srv.Close()
			inst := control.Instance{
				PID: os.Getpid(), Mode: mode, LogPath: logPath, ControlSocket: sockPath,
				ClashAPIAddr: clashAddr, ClashSecret: clashSecret, StartedAt: startedAt,
			}
			_ = control.WriteInstance(configDir, inst)
			_ = os.Chown(control.InstancePath(configDir), realUID, realGID)
			defer control.RemoveInstance(configDir)
		} else {
			fmt.Fprintln(os.Stderr, "warning: control socket unavailable:", err)
		}
	}

	// flag/env key overrides the saved profile.
	initialLink := strings.TrimSpace(opts.combinedKey())
	if initialLink == "" {
		initialLink = savedLink
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

	if opts.headless {
		if err := runHeadless(ctx, executor, notes, initialLink, opts); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	model := ui.New(executor, notes).WithLogPath(logPath)
	if initialLink != "" {
		// Remember the link: load it (no mode started — user picks PROXY/VPN) and
		// skip the input screen.
		if err := executor.LoadLink(ctx, initialLink); err == nil {
			model = model.WithLoadedProfile().WithCurrentLink(initialLink)
			switch {
			case opts.proxy:
				model = model.WithAutoMode(ui.RunProxy)
			case opts.vpn:
				model = model.WithAutoMode(ui.RunVPN)
			}
			if opts.logs {
				model = model.WithLogsOpen()
			}
		}
	}
	// WithAltScreen clears the terminal (alternate buffer) so earlier commands
	// aren't visible above the UI.
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))
	go func() {
		<-ctx.Done()
		program.Quit()
	}()

	if _, err := program.Run(); err != nil {
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
func runHeadless(ctx context.Context, executor *app.Executor, notes <-chan tea.Msg, link string, opts *options) error {
	if link == "" {
		return fmt.Errorf("headless mode needs a key: pass --key, set %s (or .env), or save a profile first", envKey)
	}
	if err := executor.LoadLink(ctx, link); err != nil {
		return fmt.Errorf("load key: %w", err)
	}

	mode := "PROXY"
	enable := executor.EnableProxy
	if opts.vpn {
		mode, enable = "VPN", executor.EnableVPN
	}
	if err := enable(ctx); err != nil {
		return fmt.Errorf("enable %s: %w", strings.ToLower(mode), err)
	}
	socks := opts.port
	if socks == 0 {
		socks = 1080
	}
	fmt.Printf("singctl %s: %s mode up — socks 127.0.0.1:%d, http 127.0.0.1:%d (ctrl+c to stop)\n",
		version, mode, socks, socks+1)

	// Route requested PIDs and/or launch a proxied command (best-effort; errors
	// are reported but do not abort the running proxy).
	pids, _ := opts.routePIDs()
	for _, pid := range pids {
		if err := executor.RoutePID(ctx, pid); err != nil {
			fmt.Fprintf(os.Stderr, "route-pid %d: %v\n", pid, err)
		} else {
			fmt.Printf("singctl: routing PID %d through the proxy\n", pid)
		}
	}
	restartPIDs, _ := opts.restartPIDs()
	for _, pid := range restartPIDs {
		if newPID, err := executor.RestartProxied(ctx, pid); err != nil {
			fmt.Fprintf(os.Stderr, "restart-pid %d: %v\n", pid, err)
		} else {
			fmt.Printf("singctl: restarted PID %d in proxy mode (new PID %d)\n", pid, newPID)
		}
	}
	if opts.launch {
		if pid, err := executor.LaunchProxied(ctx, opts.launchArgv); err != nil {
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
