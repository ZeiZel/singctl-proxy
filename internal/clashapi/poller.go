package clashapi

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Sink receives data produced on each poll tick. Any field may be nil. All
// callbacks run on the poller goroutine and must not block for long.
type Sink struct {
	// LogLine is called once per newly-seen connection with a formatted line.
	LogLine func(string)
	// Connections is called every tick with the full current connection list
	// (after process-name enrichment).
	Connections func([]Connection)
	// Proxies is called every tick with proxy/group latency states.
	Proxies func(map[string]ProxyState)
}

// Poller periodically reads the Clash API and feeds a Sink. It enriches missing
// process names via Resolve (e.g. a /proc source-port lookup on Linux) and logs
// each connection only once (deduped by connection ID).
type Poller struct {
	Client   *Client
	Interval time.Duration
	Resolve  func(srcPort int) string // optional process-name fallback
	Sink     Sink
}

// Run polls until ctx is cancelled. It does a first poll immediately so the UI
// and log populate without waiting a full interval.
func (p *Poller) Run(ctx context.Context) {
	interval := p.Interval
	if interval <= 0 {
		interval = time.Second
	}
	seen := make(map[string]bool)
	tick := time.NewTicker(interval)
	defer tick.Stop()
	p.poll(ctx, seen)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			p.poll(ctx, seen)
		}
	}
}

func (p *Poller) poll(ctx context.Context, seen map[string]bool) {
	conns, err := p.Client.Connections(ctx)
	if err == nil {
		p.enrich(conns)
		if p.Sink.LogLine != nil {
			for _, c := range conns {
				if c.ID != "" && seen[c.ID] {
					continue
				}
				if c.ID != "" {
					seen[c.ID] = true
				}
				p.Sink.LogLine(FormatLine(time.Now(), c))
			}
			pruneSeen(seen, conns)
		}
		if p.Sink.Connections != nil {
			p.Sink.Connections(conns)
		}
	}
	if p.Sink.Proxies != nil {
		if proxies, err := p.Client.Proxies(ctx); err == nil {
			p.Sink.Proxies(proxies)
		}
	}
}

// enrich fills empty process names using the Resolve fallback keyed on the
// connection's source port.
func (p *Poller) enrich(conns []Connection) {
	if p.Resolve == nil {
		return
	}
	for i := range conns {
		if conns[i].Metadata.Process != "" {
			continue
		}
		if name := p.Resolve(conns[i].Metadata.SourcePortNum()); name != "" {
			conns[i].Metadata.Process = name
		}
	}
}

// pruneSeen drops IDs no longer present so the dedup map cannot grow unbounded.
func pruneSeen(seen map[string]bool, conns []Connection) {
	live := make(map[string]bool, len(conns))
	for _, c := range conns {
		live[c.ID] = true
	}
	for id := range seen {
		if !live[id] {
			delete(seen, id)
		}
	}
}

// FormatLine renders one connection as a single enriched log line:
//
//	15:04:05  codex  127.0.0.1:54321 → api.openai.com:443  [tcp]  via proxy-0
//
// It is pure so it can be unit-tested.
func FormatLine(now time.Time, c Connection) string {
	proc := c.Metadata.Process
	if proc == "" {
		proc = "?"
	}
	chain := strings.Join(c.Chains, "→")
	if chain == "" {
		chain = "?"
	}
	network := c.Metadata.Network
	if network == "" {
		network = "?"
	}
	return fmt.Sprintf("%s  %s  %s → %s  [%s]  via %s",
		now.Format("15:04:05"), proc, c.Metadata.Source(), c.Metadata.Dest(), network, chain)
}
