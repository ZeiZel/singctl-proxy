// Package remote implements ui.Backend by driving an already-running singctl
// instance over its control socket, instead of starting local sing-box cores.
// It is used when a second invocation detects a live instance: mode switches,
// settings and keys go to the daemon via the socket; per-process routing and the
// process list run locally (same machine, pointed at the daemon's socks port);
// connections + latency come from the daemon's Clash API; logs are tailed from
// the daemon's log file (handled by the UI via WithLogPath).
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"singctl/internal/clashapi"
	"singctl/internal/clashui"
	"singctl/internal/control"
	"singctl/internal/proclist"
	"singctl/internal/procproxy"
	"singctl/internal/ui"
)

const pollInterval = 2 * time.Second

// Backend drives a remote instance. It satisfies ui.Backend.
type Backend struct {
	sock        string
	clashAddr   string
	clashSecret string
	socksPort   int
	notes       chan<- tea.Msg

	routerOnce sync.Once
	router     procproxy.Router
	launchUser *procproxy.LaunchUser
	listerOnce sync.Once
	lister     proclist.Lister

	pollMu     sync.Mutex
	pollCancel context.CancelFunc

	consoleCancel context.CancelFunc
}

// New builds a remote backend for an advertised instance. socksPort is where the
// daemon's SOCKS listener is (for local per-process routing); notes is the UI's
// channel. It starts polling the daemon's Clash API immediately.
func New(inst control.Instance, socksPort int, notes chan<- tea.Msg) *Backend {
	if socksPort == 0 {
		socksPort = 1080
	}
	b := &Backend{
		sock:        inst.ControlSocket,
		clashAddr:   inst.ClashAPIAddr,
		clashSecret: inst.ClashSecret,
		socksPort:   socksPort,
		notes:       notes,
	}
	b.startPoller()
	b.startConsolePoller()
	return b
}

// consoleEntry mirrors app.ConsoleEntry for decoding CONSOLE-POLL replies (kept
// local so remote stays decoupled from package app).
type consoleEntry struct {
	ID     int    `json:"id"`
	PID    int    `json:"pid"`
	App    string `json:"app"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// startConsolePoller tails the daemon's per-app console over the control socket
// (CONSOLE-POLL <sinceID>) and pushes each line to the UI as a ConsoleMsg, so an
// attached client sees the sub-logs of apps launched inside the daemon.
func (b *Backend) startConsolePoller() {
	ctx, cancel := context.WithCancel(context.Background())
	b.consoleCancel = cancel
	go func() {
		last := 0
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				reply, err := control.Request(b.sock, "CONSOLE-POLL", strconv.Itoa(last))
				if err != nil {
					continue
				}
				var entries []consoleEntry
				if json.Unmarshal([]byte(reply), &entries) != nil {
					continue
				}
				for _, e := range entries {
					if e.ID > last {
						last = e.ID
					}
					b.push(ui.ConsoleMsg{PID: e.PID, App: e.App, Stream: e.Stream, Text: e.Text})
				}
			}
		}
	}()
}

// --- ui.Backend: mode / keys / settings over the control socket ---

func (b *Backend) EnableProxy(context.Context) error { return b.mode("proxy") }
func (b *Backend) EnableVPN(context.Context) error   { return b.mode("vpn") }
func (b *Backend) Stop(context.Context) error        { return b.mode("off") }

func (b *Backend) mode(m string) error {
	_, err := control.Request(b.sock, "MODE", m)
	return err
}

func (b *Backend) LoadLink(_ context.Context, link string) error { return b.add(link) }
func (b *Backend) AddLink(_ context.Context, link string) error  { return b.add(link) }

func (b *Backend) add(link string) error {
	_, err := control.Request(b.sock, "KEYS-ADD", strings.TrimSpace(link))
	return err
}

func (b *Backend) DeleteLink(_ context.Context, index int) error {
	_, err := control.Request(b.sock, "KEYS-REMOVE", strconv.Itoa(index))
	return err
}

func (b *Backend) RenameLink(_ context.Context, index int, name string) error {
	_, err := control.Request(b.sock, "KEYS-RENAME", strconv.Itoa(index)+" "+strings.TrimSpace(name))
	return err
}

func (b *Backend) CurrentLinks() []string {
	reply, err := control.Request(b.sock, "KEYS-GET", "")
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(reply, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (b *Backend) ApplySettings(_ context.Context, s ui.Settings) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if _, err := control.Request(b.sock, "SETTINGS-SET", string(data)); err != nil {
		return err
	}
	if s.SocksPort != 0 && s.SocksPort != b.socksPort {
		b.socksPort = s.SocksPort
		b.resetRouter()
	}
	return nil
}

// Settings fetches the daemon's current settings (for seeding the UI form).
func (b *Backend) Settings() (ui.Settings, error) {
	reply, err := control.Request(b.sock, "SETTINGS-GET", "")
	if err != nil {
		return ui.Settings{}, err
	}
	var s ui.Settings
	if err := json.Unmarshal([]byte(reply), &s); err != nil {
		return ui.Settings{}, err
	}
	return s, nil
}

// Status fetches the daemon's status (for the attach badge / display mode).
func (b *Backend) Status() (control.Status, error) { return control.QueryStatus(b.sock) }

func (b *Backend) Daemonize(context.Context) error {
	return errors.New("уже запущено в фоне — это подключение к работающему инстансу")
}

func (b *Backend) StopDaemon(context.Context) error { return control.Stop(b.sock) }

// SetLaunchUser records the real (non-root) user that locally launched/restarted
// children should run as (relevant only if the attach client itself runs under
// sudo). Must be called before the first routing action (the router is built
// lazily, once).
func (b *Backend) SetLaunchUser(u *procproxy.LaunchUser) { b.launchUser = u }

// --- ui.Backend: per-process routing runs LOCALLY toward the daemon's port ---

func (b *Backend) procRouter() procproxy.Router {
	b.routerOnce.Do(func() {
		b.router = procproxy.NewRouter(procproxy.Config{
			SocksAddr:  fmt.Sprintf("127.0.0.1:%d", b.socksPort),
			HTTPAddr:   fmt.Sprintf("127.0.0.1:%d", b.socksPort+1),
			LaunchUser: b.launchUser,
			Output: procproxy.SinkFunc(func(l procproxy.OutputLine) {
				b.push(ui.ConsoleMsg{PID: l.PID, App: l.App, Stream: l.Stream, Text: l.Text})
			}),
		})
	})
	return b.router
}

func (b *Backend) resetRouter() {
	if b.router != nil {
		_ = b.router.Cleanup()
	}
	b.router = nil
	b.routerOnce = sync.Once{}
}

func (b *Backend) RoutePID(ctx context.Context, pid int) error {
	return b.procRouter().AddPID(ctx, pid)
}

func (b *Backend) LaunchProxied(ctx context.Context, argv []string) (int, error) {
	return b.procRouter().Launch(ctx, argv)
}

func (b *Backend) RestartProxied(ctx context.Context, pid int) (int, error) {
	return b.procRouter().RestartPID(ctx, pid)
}

// UnroutePID / StopProxied act on children launched by THIS attach client (they
// run locally toward the daemon's port), so they use the local router directly.
func (b *Backend) UnroutePID(ctx context.Context, pid int) error {
	return b.procRouter().Unroute(ctx, pid)
}

func (b *Backend) StopProxied(ctx context.Context, pid int) error {
	return b.procRouter().Kill(ctx, pid)
}

// MarkIntroSeen is a no-op for an attached client (the intro is a first-run,
// local concern; the daemon owns no terminal).
func (b *Backend) MarkIntroSeen() error { return nil }

func (b *Backend) ListProcesses(ctx context.Context) ([]ui.ProcInfo, error) {
	b.listerOnce.Do(func() { b.lister = proclist.NewLister() })
	procs, err := b.lister.List(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]ui.ProcInfo, 0, len(procs))
	for _, p := range procs {
		rows = append(rows, ui.ProcInfo{PID: p.PID, Name: p.Name, Ports: p.PortsString(), Children: p.Children})
	}
	return rows, nil
}

// --- Clash API poller against the daemon (connections + latency) ---

func (b *Backend) startPoller() {
	if b.clashAddr == "" {
		return
	}
	b.pollMu.Lock()
	defer b.pollMu.Unlock()
	if b.pollCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.pollCancel = cancel
	p := &clashapi.Poller{
		Client:   clashapi.NewClient(b.clashAddr, b.clashSecret),
		Interval: pollInterval,
		Resolve:  clashapi.NewProcessResolver(),
		Sink: clashapi.Sink{
			// No LogLine: the daemon already writes the log file the UI tails.
			Connections: func(c []clashapi.Connection) { b.push(ui.ConnectionsMsg{Rows: clashui.ConnRows(c)}) },
			Proxies: func(m map[string]clashapi.ProxyState) {
				if msg, ok := clashui.LatencyMsg(m); ok {
					b.push(msg)
				}
			},
		},
	}
	go p.Run(ctx)
}

func (b *Backend) push(msg tea.Msg) {
	if b.notes == nil {
		return
	}
	select {
	case b.notes <- msg:
	default:
	}
}

// Close stops the poller and cleans up the local router.
func (b *Backend) Close() {
	b.pollMu.Lock()
	if b.pollCancel != nil {
		b.pollCancel()
		b.pollCancel = nil
	}
	if b.consoleCancel != nil {
		b.consoleCancel()
		b.consoleCancel = nil
	}
	b.pollMu.Unlock()
	if b.router != nil {
		_ = b.router.Cleanup()
	}
}
