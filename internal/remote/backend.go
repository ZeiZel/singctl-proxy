// Package remote drives an already-running singctl instance over its control
// socket, instead of starting local sing-box cores. It is used when a second
// invocation detects a live instance: mode switches, settings and keys go to
// the daemon via the socket; per-process routing and the process list run
// locally (same machine, pointed at the daemon's socks port); connections +
// latency come from the daemon's Clash API; logs are tailed from the daemon's
// log file.
package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"singctl/internal/clashapi"
	"singctl/internal/clashui"
	"singctl/internal/control"
	"singctl/internal/notify"
	"singctl/internal/proclist"
	"singctl/internal/procproxy"
)

const pollInterval = 2 * time.Second

// Backend drives a remote instance.
type Backend struct {
	sock        string
	clashAddr   string
	clashSecret string
	socksPort   int
	notes       chan<- any

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
func New(inst control.Instance, socksPort int, notes chan<- any) *Backend {
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
					b.push(notify.ConsoleMsg{PID: e.PID, App: e.App, Stream: e.Stream, Text: e.Text})
				}
			}
		}
	}()
}

// --- mode / keys / settings over the control socket ---

func (b *Backend) EnableProxy(context.Context) error { return b.mode("proxy") }
func (b *Backend) EnableVPN(context.Context) error   { return b.mode("vpn") }
func (b *Backend) Stop(context.Context) error        { return b.mode("off") }

func (b *Backend) mode(m string) error {
	_, err := control.Request(b.sock, "MODE", m)
	return err
}

func (b *Backend) LoadLink(_ context.Context, link string) error { return b.add(link) }
func (b *Backend) AddLink(_ context.Context, link string) error  { return b.add(link) }

// add sends a key to the daemon. A single-line share link goes through
// KEYS-ADD unchanged; a multi-line key (a WireGuard config — so far the only
// config-input protocol) cannot survive KEYS-ADD's one-line-per-request
// framing (see control.HandlerFunc's doc comment), so it goes through
// KEYS-ADD-CONFIG instead, base64-encoded.
func (b *Backend) add(key string) error {
	if strings.ContainsAny(key, "\n\r") {
		enc := base64.StdEncoding.EncodeToString([]byte(key))
		_, err := control.Request(b.sock, "KEYS-ADD-CONFIG", enc)
		return err
	}
	_, err := control.Request(b.sock, "KEYS-ADD", strings.TrimSpace(key))
	return err
}

func (b *Backend) DeleteLink(_ context.Context, index int) error {
	_, err := control.Request(b.sock, "KEYS-REMOVE", strconv.Itoa(index))
	return staleDaemon(err)
}

func (b *Backend) RenameLink(_ context.Context, index int, name string) error {
	_, err := control.Request(b.sock, "KEYS-RENAME", strconv.Itoa(index)+" "+strings.TrimSpace(name))
	return staleDaemon(err)
}

// ProxyGroup mirrors app.ProxyGroup, kept as a local type (like consoleEntry
// above) so remote stays decoupled from package app.
type ProxyGroup struct {
	Available bool          `json:"available"`
	Auto      bool          `json:"auto"`
	Selected  string        `json:"selected"`
	Members   []ProxyMember `json:"members"`
}

// ProxyMember mirrors app.ProxyMember.
type ProxyMember struct {
	Tag   string `json:"tag"`
	Index int    `json:"index"`
	Name  string `json:"name"`
	Delay int    `json:"delay"`
}

// ProxyGroup fetches the daemon's multi-server failover group (PROXY-GROUP).
func (b *Backend) ProxyGroup(context.Context) (ProxyGroup, error) {
	reply, err := control.Request(b.sock, "PROXY-GROUP", "")
	if err != nil {
		return ProxyGroup{}, staleDaemon(err)
	}
	var g ProxyGroup
	if err := json.Unmarshal([]byte(reply), &g); err != nil {
		return ProxyGroup{}, err
	}
	return g, nil
}

// SelectProxy pins the daemon's failover group to tag ("auto" or "proxy-N")
// via PROXY-SELECT.
func (b *Backend) SelectProxy(_ context.Context, tag string) error {
	_, err := control.Request(b.sock, "PROXY-SELECT", strings.TrimSpace(tag))
	return staleDaemon(err)
}

// staleDaemon rewrites the control socket's "unknown command" reply (which means
// the running daemon is an older binary than this client) into actionable advice.
func staleDaemon(err error) error {
	if err != nil && strings.Contains(err.Error(), "unknown command") {
		return errors.New("the running daemon is older than this client and doesn't know this command — update it: " +
			"`make install` (reinstalls and restarts the daemon) or `sudo singctl --stop` and start it again")
	}
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

func (b *Backend) ApplySettings(_ context.Context, s notify.Settings) error {
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
func (b *Backend) Settings() (notify.Settings, error) {
	reply, err := control.Request(b.sock, "SETTINGS-GET", "")
	if err != nil {
		return notify.Settings{}, err
	}
	var s notify.Settings
	if err := json.Unmarshal([]byte(reply), &s); err != nil {
		return notify.Settings{}, err
	}
	return s, nil
}

// Status fetches the daemon's status (for the attach badge / display mode).
func (b *Backend) Status() (control.Status, error) { return control.QueryStatus(b.sock) }

func (b *Backend) Daemonize(context.Context) error {
	return errors.New("already running in the background — this is a connection to a running instance")
}

func (b *Backend) StopDaemon(context.Context) error { return control.Stop(b.sock) }

// SetLaunchUser records the real (non-root) user that locally launched/restarted
// children should run as (relevant only if the attach client itself runs under
// sudo). Must be called before the first routing action (the router is built
// lazily, once).
func (b *Backend) SetLaunchUser(u *procproxy.LaunchUser) { b.launchUser = u }

// --- per-process routing runs LOCALLY toward the daemon's port ---

func (b *Backend) procRouter() procproxy.Router {
	b.routerOnce.Do(func() {
		b.router = procproxy.NewRouter(procproxy.Config{
			SocksAddr:  fmt.Sprintf("127.0.0.1:%d", b.socksPort),
			HTTPAddr:   fmt.Sprintf("127.0.0.1:%d", b.socksPort+1),
			LaunchUser: b.launchUser,
			Output: procproxy.SinkFunc(func(l procproxy.OutputLine) {
				b.push(notify.ConsoleMsg{PID: l.PID, App: l.App, Stream: l.Stream, Text: l.Text})
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

func (b *Backend) ListProcesses(ctx context.Context) ([]notify.ProcInfo, error) {
	b.listerOnce.Do(func() { b.lister = proclist.NewLister() })
	procs, err := b.lister.List(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]notify.ProcInfo, 0, len(procs))
	for _, p := range procs {
		rows = append(rows, notify.ProcInfo{PID: p.PID, Name: p.Name, Ports: p.PortsString(), Children: p.Children})
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
			Connections: func(c []clashapi.Connection) { b.push(notify.ConnectionsMsg{Rows: clashui.ConnRows(c)}) },
			Proxies: func(m map[string]clashapi.ProxyState) {
				if msg, ok := clashui.LatencyMsg(m); ok {
					b.push(msg)
				}
			},
		},
	}
	go p.Run(ctx)
}

func (b *Backend) push(msg any) {
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
