package bridge

import (
	"errors"
	"strconv"
	"strings"

	"singctl/internal/clashapi"
)

// errNoDaemon is returned by every action when no live daemon is found. The
// frontend treats it as the "service not running" state.
var errNoDaemon = errors.New("singctl daemon is not running")

// Status is the daemon/runtime status surfaced to the GUI (Wails generates the
// matching TS type from this struct).
type Status struct {
	Running   bool   `json:"running"`
	PID       int    `json:"pid"`
	Mode      string `json:"mode"` // "off" | "proxy" | "vpn" | "suspended"
	StartedAt string `json:"startedAt"`
	ClashAPI  bool   `json:"clashApi"` // whether the Clash API is enabled (charts available)
	// Cisco-coexistence state. When CiscoActive, ProxyBypass reports whether the
	// proxy egress is pinned to PhysIface (the physical NIC) to bypass Cisco, vs.
	// riding it (fallback).
	CiscoActive bool   `json:"ciscoActive"`
	ProxyBypass bool   `json:"proxyBypass"`
	PhysIface   string `json:"physIface"`
}

// Settings mirrors the daemon's ui.Settings JSON wire format (SETTINGS-GET/SET).
// Field names match exactly so the same JSON marshals on both ends.
type Settings struct {
	SocksPort        int    `json:"SocksPort"`
	ClashEnabled     bool   `json:"ClashEnabled"`
	ClashAddr        string `json:"ClashAddr"`
	URLTestURL       string `json:"URLTestURL"`
	URLTestInterval  string `json:"URLTestInterval"`
	URLTestTolerance int    `json:"URLTestTolerance"`
	SaveProfile      bool   `json:"SaveProfile"`
}

// Key is one loaded VLESS key, masked for display (the raw secret is never sent
// to the frontend; only the #fragment label and a masked preview).
type Key struct {
	Index  int    `json:"index"`
	Name   string `json:"name"`   // #fragment label, or "Ключ N"
	Masked string `json:"masked"` // e.g. "vless://••••@host:443"
}

// ProcInfo mirrors the daemon's ui.ProcInfo JSON (PROC-LIST). Field names match.
type ProcInfo struct {
	PID      int    `json:"PID"`
	Name     string `json:"Name"`
	Ports    string `json:"Ports"`
	Children int    `json:"Children"`
}

// ConnRow is one live connection for the connections table.
type ConnRow struct {
	Process string `json:"process"`
	Source  string `json:"source"`
	Dest    string `json:"dest"`
	Network string `json:"network"`
	Chain   string `json:"chain"`
}

// LatencyRow is one server's measured latency in the failover group.
type LatencyRow struct {
	Tag      string `json:"tag"`
	Delay    int    `json:"delay"` // ms; 0 = timed out / unknown
	Selected bool   `json:"selected"`
}

// Latency is the per-server latency snapshot + selected server.
type Latency struct {
	Selected string       `json:"selected"`
	Rows     []LatencyRow `json:"rows"`
}

// TrafficEvent is emitted on the "traffic" Wails event: cumulative totals plus
// per-second rates computed from the previous sample.
type TrafficEvent struct {
	Up       int64 `json:"up"`       // cumulative bytes
	Down     int64 `json:"down"`     // cumulative bytes
	UpRate   int64 `json:"upRate"`   // bytes/sec since last sample
	DownRate int64 `json:"downRate"` // bytes/sec since last sample
}

// ConsoleLine is one captured stdout/stderr line from a proxied app (CONSOLE-POLL).
type ConsoleLine struct {
	ID     int    `json:"id"`
	PID    int    `json:"pid"`
	App    string `json:"app"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// connRows converts Clash API connections into table rows (mirrors clashui.ConnRows).
func connRows(conns []clashapi.Connection) []ConnRow {
	rows := make([]ConnRow, 0, len(conns))
	for _, c := range conns {
		rows = append(rows, ConnRow{
			Process: c.Metadata.Process,
			Source:  c.Metadata.Source(),
			Dest:    c.Metadata.Dest(),
			Network: c.Metadata.Network,
			Chain:   strings.Join(c.Chains, "→"),
		})
	}
	return rows
}

// latencyFrom extracts the failover group's latency + selection from /proxies
// (mirrors clashui.LatencyMsg). The group is named "proxy"; ok is false when
// absent.
func latencyFrom(proxies map[string]clashapi.ProxyState) (Latency, bool) {
	group, present := proxies["proxy"]
	if !present {
		return Latency{}, false
	}
	var out Latency
	if len(group.All) > 0 { // urltest group (multi-server)
		out.Selected = group.Now
		for _, tag := range group.All {
			out.Rows = append(out.Rows, LatencyRow{Tag: tag, Delay: proxies[tag].LastDelay(), Selected: tag == group.Now})
		}
	} else { // single server
		out.Selected = "proxy"
		out.Rows = []LatencyRow{{Tag: "proxy", Delay: group.LastDelay(), Selected: true}}
	}
	return out, true
}

// maskKey turns a raw VLESS link into a display-safe masked form, keeping the
// scheme + host:port and the #fragment label but hiding the UUID/secret.
func maskKey(raw string, index int) Key {
	name := ""
	if i := strings.LastIndex(raw, "#"); i >= 0 {
		name = raw[i+1:]
	}
	if name == "" {
		name = "Key " + strconv.Itoa(index+1)
	}
	masked := "vless://••••"
	// Keep the @host:port tail (after the credential) for recognisability.
	if at := strings.Index(raw, "@"); at >= 0 {
		tail := raw[at:]
		if h := strings.IndexByte(tail, '#'); h >= 0 {
			tail = tail[:h]
		}
		if q := strings.IndexByte(tail, '?'); q >= 0 {
			tail = tail[:q]
		}
		masked = "vless://••••" + tail
	}
	return Key{Index: index, Name: name, Masked: masked}
}
