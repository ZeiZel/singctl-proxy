// Command singctl is a terminal UI that runs a VLESS proxy on an embedded
// sing-box core and toggles a system VPN (TUN) mode, while passively coexisting
// with Cisco Secure Client (observe-only). Build the shipping binary with
// `-tags singbox`; the default build links a stub core for the hermetic tests.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"singctl/internal/app"
	"singctl/internal/core"
	"singctl/internal/monitor"
	"singctl/internal/netstate"
	"singctl/internal/platform"
	"singctl/internal/profile"
	"singctl/internal/runtime"
	"singctl/internal/types"
	"singctl/internal/ui"
)

const pollInterval = 2 * time.Second

// requireRoot enforces that we run under sudo (TUN/auto_route need privileges).
func requireRoot(euid int) error {
	if euid != 0 {
		return fmt.Errorf("singctl must run as root — re-run with: sudo singctl")
	}
	return nil
}

func main() {
	if err := requireRoot(os.Geteuid()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Read-only passive detector + physical-interface prober + orphan cleanup.
	detector := netstate.New(netstate.NewOSRunner())
	prober := runtime.NewNetProber(detector)
	routes := runtime.NewOSRouteController()

	notes := make(chan tea.Msg, 32)
	executor := app.NewExecutor(core.NewFactory(), prober, routes, notes)

	// Persist the profile + log file under the real user's home (chowned back),
	// and remember the last link.
	var initialLink, logPath string
	if ru, err := platform.ResolveUser(os.Getenv, user.Lookup); err == nil {
		configDir := filepath.Join(ru.HomeDir, ".config", "singctl")
		_ = os.MkdirAll(configDir, 0o755)
		_ = os.Chown(configDir, ru.Uid, ru.Gid)
		logPath = filepath.Join(configDir, "singbox.log")

		store := profile.NewStore(profile.OSFS{}, ru.HomeDir, ru.Uid, ru.Gid)
		executor.SetSaver(store.Save)
		executor.SetLogPath(logPath)
		if l, err := store.Load(); err == nil {
			initialLink = l
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

	model := ui.New(executor, notes).WithLogPath(logPath)
	if initialLink != "" {
		// Remember the link: load it (no mode started — user picks PROXY/VPN) and
		// skip the input screen.
		if err := executor.LoadLink(ctx, initialLink); err == nil {
			model = model.WithLoadedProfile()
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
