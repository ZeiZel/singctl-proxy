package bridge

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"singctl/internal/clashapi"
	"singctl/internal/control"
)

// pollInterval matches the daemon's own Clash API poll cadence.
const pollInterval = 2 * time.Second

// poller drives the live-data Wails events. It polls the daemon's Clash API for
// connections/latency/traffic and the control socket for per-app console output,
// emitting events the React frontend subscribes to:
//
//	"status"      -> Status        (running/mode changes)
//	"traffic"     -> TrafficEvent  (cumulative + per-second rates)
//	"connections" -> []ConnRow
//	"latency"     -> Latency
//	"console"     -> []ConsoleLine
type poller struct {
	ctx    context.Context
	daemon *Daemon
	cancel context.CancelFunc

	lastUp, lastDown int64
	haveLast         bool
	lastConsoleID    int
	lastStatus       Status
}

func newPoller(ctx context.Context, d *Daemon) *poller {
	return &poller{ctx: ctx, daemon: d}
}

// start launches the polling loop in a goroutine.
func (p *poller) start() {
	ctx, cancel := context.WithCancel(p.ctx)
	p.cancel = cancel
	go p.loop(ctx)
}

// stop ends the polling loop.
func (p *poller) stop() {
	if p.cancel != nil {
		p.cancel()
	}
}

func (p *poller) loop(ctx context.Context) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	p.tick(ctx) // immediate first poll so the UI populates without waiting
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.tick(ctx)
		}
	}
}

// tick performs one poll cycle, emitting whatever data is available. It is
// resilient to a missing/restarted daemon (no daemon → status event only).
func (p *poller) tick(ctx context.Context) {
	inst, live := p.daemon.resolve()

	// Status event (always, so the UI reflects daemon presence + mode).
	st := Status{Running: live, Mode: "off"}
	if live {
		st = Status{Running: true, PID: inst.PID, Mode: inst.Mode, StartedAt: inst.StartedAt, ClashAPI: inst.ClashAPIAddr != ""}
		if reply, err := control.Request(inst.ControlSocket, "STATUS", ""); err == nil {
			var s control.Status
			if json.Unmarshal([]byte(reply), &s) == nil && s.Mode != "" {
				st.Mode, st.PID = s.Mode, s.PID
			}
		}
	}
	if st != p.lastStatus {
		p.emit("status", st)
		p.lastStatus = st
	}
	if !live {
		p.haveLast = false
		return
	}

	// Console output (per-app stdout/stderr) over the control socket.
	if reply, err := control.Request(inst.ControlSocket, "CONSOLE-POLL", strconv.Itoa(p.lastConsoleID)); err == nil {
		var lines []ConsoleLine
		if json.Unmarshal([]byte(reply), &lines) == nil && len(lines) > 0 {
			for _, l := range lines {
				if l.ID > p.lastConsoleID {
					p.lastConsoleID = l.ID
				}
			}
			p.emit("console", lines)
		}
	}

	// Clash API: connections + latency + traffic (only when enabled).
	if inst.ClashAPIAddr == "" {
		return
	}
	client := clashapi.NewClient(inst.ClashAPIAddr, inst.ClashSecret)

	if conns, err := client.Connections(ctx); err == nil {
		p.emit("connections", connRows(conns))
	}
	if proxies, err := client.Proxies(ctx); err == nil {
		if lat, ok := latencyFrom(proxies); ok {
			p.emit("latency", lat)
		}
	}
	if up, down, err := client.Traffic(ctx); err == nil {
		ev := TrafficEvent{Up: up, Down: down}
		if p.haveLast {
			// Rates over the poll interval (guard against counter resets on reload).
			secs := int64(pollInterval / time.Second)
			if secs < 1 {
				secs = 1
			}
			if up >= p.lastUp {
				ev.UpRate = (up - p.lastUp) / secs
			}
			if down >= p.lastDown {
				ev.DownRate = (down - p.lastDown) / secs
			}
		}
		p.lastUp, p.lastDown, p.haveLast = up, down, true
		p.emit("traffic", ev)
	}
}

// emit sends a Wails event to the frontend (no-op if the context is gone).
func (p *poller) emit(name string, data any) {
	if p.ctx == nil {
		return
	}
	wruntime.EventsEmit(p.ctx, name, data)
}
