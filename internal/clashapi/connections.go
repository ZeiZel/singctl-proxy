package clashapi

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// State names WHY the connection table is what it is (docs/v2-spec.md F6 item
// 3). "No active connections" used to be asserted unconditionally, which is
// exactly the bug reported: it cannot distinguish nothing running, the API
// being unreachable, the API being switched off, or a healthy proxy simply
// carrying no traffic right now. Connections (below) is the one place that
// decides this, so the Dashboard's "Enable the Clash API" hint and the
// Connections screen can never again disagree about which is true.
type State string

const (
	// StateNoMode means no proxy/VPN mode is running at all.
	StateNoMode State = "no_mode"
	// StateAPIDisabled means a mode is up but the Clash API is switched off
	// in Settings.
	StateAPIDisabled State = "api_disabled"
	// StateAPIUnreachable means a mode is up and the API is enabled, but the
	// HTTP request to it failed. Detail carries the address tried and the error.
	StateAPIUnreachable State = "api_unreachable"
	// StateIdle means everything is fine — the API answered, there is simply
	// no traffic right now.
	StateIdle State = "idle"
	// StateActive means the API answered with at least one live connection.
	StateActive State = "active"
)

// Row is one live connection, carrying everything the Connections screen
// needs to render a row and explain why it went where it did (F6 items 1 and
// 6): a stable id, a friendly app name, the destination, the rule/chain that
// routed it, byte counters, and a start time to derive a duration from.
type Row struct {
	ID       string    `json:"id"`
	App      string    `json:"app"`
	Process  string    `json:"process,omitempty"`
	Host     string    `json:"host"`
	Port     string    `json:"port,omitempty"`
	Network  string    `json:"network,omitempty"`
	Rule     string    `json:"rule,omitempty"`
	Chain    []string  `json:"chain,omitempty"`
	Upload   int64     `json:"upload"`
	Download int64     `json:"download"`
	Start    time.Time `json:"start"`
}

// AppTotal is one application's aggregate across every connection currently
// attributed to it (F6 item 2).
type AppTotal struct {
	App      string `json:"app"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
	Count    int    `json:"count"`
}

// DestTotal is one destination host's aggregate across every connection
// currently dialing it (F6 item 2).
type DestTotal struct {
	Host     string `json:"host"`
	Upload   int64  `json:"upload"`
	Download int64  `json:"download"`
	Count    int    `json:"count"`
}

// Payload is the CONNECTIONS control command's reply: the live table, its
// per-app/per-destination aggregates, and — the important part — an explicit
// State (plus Detail for api_unreachable) so an empty Rows can never again be
// mistaken for one of the other three reasons (F6 item 3).
type Payload struct {
	State  State       `json:"state"`
	Rows   []Row       `json:"rows"`
	Apps   []AppTotal  `json:"apps"`
	Dests  []DestTotal `json:"dests"`
	Detail string      `json:"detail,omitempty"`
}

// AppName resolves a friendly application name from Clash-reported process
// metadata: the executable's base name from ProcessPath (the closest thing to
// a display name available without platform-specific bundle-ID/display-name
// lookup machinery, which does not belong in this read-only HTTP client),
// falling back to the bare Process name, else "" when neither is present —
// see BuildRows, which is exercised against a connection with no process
// metadata at all.
func AppName(m Metadata) string {
	if m.ProcessPath != "" {
		base := m.ProcessPath
		if i := strings.LastIndexAny(base, `/\`); i >= 0 {
			base = base[i+1:]
		}
		if base != "" {
			return base
		}
	}
	return m.Process
}

// hostOnly returns the destination host without its port (Metadata.Dest
// already joins host:port, which Row keeps separate so the UI can sort/filter
// on either independently).
func hostOnly(m Metadata) string {
	if m.Host != "" {
		return m.Host
	}
	return m.DestinationIP
}

// BuildRows converts live Clash API connections into display rows. A
// connection with no process metadata at all still gets a row — App and
// Process are simply empty, never a placeholder that would corrupt
// aggregation (see Aggregate, which buckets an empty App under "?").
func BuildRows(conns []Connection) []Row {
	rows := make([]Row, 0, len(conns))
	for _, c := range conns {
		rows = append(rows, Row{
			ID:       c.ID,
			App:      AppName(c.Metadata),
			Process:  c.Metadata.Process,
			Host:     hostOnly(c.Metadata),
			Port:     c.Metadata.DestinationPort,
			Network:  c.Metadata.Network,
			Rule:     c.Rule,
			Chain:    c.Chains,
			Upload:   c.Upload,
			Download: c.Download,
			Start:    c.Start,
		})
	}
	return rows
}

// Aggregate sums rows per application and per destination host (F6 item 2),
// sorted by name for a stable, deterministic response. A row with no
// resolvable app/host is bucketed under "?" rather than dropped, so the
// totals still account for every byte.
func Aggregate(rows []Row) (apps []AppTotal, dests []DestTotal) {
	appIdx := make(map[string]int, len(rows))
	destIdx := make(map[string]int, len(rows))
	for _, r := range rows {
		appKey := r.App
		if appKey == "" {
			appKey = "?"
		}
		if i, ok := appIdx[appKey]; ok {
			apps[i].Upload += r.Upload
			apps[i].Download += r.Download
			apps[i].Count++
		} else {
			appIdx[appKey] = len(apps)
			apps = append(apps, AppTotal{App: appKey, Upload: r.Upload, Download: r.Download, Count: 1})
		}

		destKey := r.Host
		if destKey == "" {
			destKey = "?"
		}
		if i, ok := destIdx[destKey]; ok {
			dests[i].Upload += r.Upload
			dests[i].Download += r.Download
			dests[i].Count++
		} else {
			destIdx[destKey] = len(dests)
			dests = append(dests, DestTotal{Host: destKey, Upload: r.Upload, Download: r.Download, Count: 1})
		}
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].App < apps[j].App })
	sort.Slice(dests, func(i, j int) bool { return dests[i].Host < dests[j].Host })
	return apps, dests
}

// Connections builds the CONNECTIONS control command's payload, deciding
// between the four empty-state diagnoses (F6 item 3):
//
//   - modeRunning == false            -> StateNoMode
//   - apiEnabled == false (or client == nil) -> StateAPIDisabled
//   - the HTTP request to client fails -> StateAPIUnreachable (Detail names
//     the address tried and the error)
//   - the request succeeds with zero rows -> StateIdle
//   - the request succeeds with at least one row -> StateActive, with the
//     full row/aggregate payload
//
// modeRunning and apiEnabled are the executor's own state (whether a
// proxy/VPN mode is up, and whether Settings has the Clash API turned on) —
// this function does not (and must not) re-derive them, so the daemon's
// Dashboard hint and this payload can never disagree about which is true.
func Connections(ctx context.Context, client *Client, modeRunning, apiEnabled bool) Payload {
	if !modeRunning {
		return Payload{State: StateNoMode}
	}
	if !apiEnabled || client == nil {
		return Payload{State: StateAPIDisabled}
	}
	conns, err := client.Connections(ctx)
	if err != nil {
		return Payload{State: StateAPIUnreachable, Detail: fmt.Sprintf("%s: %v", client.BaseURL, err)}
	}
	rows := BuildRows(conns)
	if len(rows) == 0 {
		return Payload{State: StateIdle, Rows: rows}
	}
	apps, dests := Aggregate(rows)
	return Payload{State: StateActive, Rows: rows, Apps: apps, Dests: dests}
}
