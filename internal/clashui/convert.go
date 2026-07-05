// Package clashui converts Clash API data into the notify package's display
// types. It is the one place that bridges internal/clashapi and internal/notify,
// so both the local Executor and the remote attach backend share identical
// conversion.
package clashui

import (
	"strings"

	"singctl/internal/clashapi"
	"singctl/internal/notify"
)

// ConnRows converts live Clash API connections into notify rows.
func ConnRows(conns []clashapi.Connection) []notify.ConnRow {
	rows := make([]notify.ConnRow, 0, len(conns))
	for _, c := range conns {
		rows = append(rows, notify.ConnRow{
			Process: c.Metadata.Process,
			Source:  c.Metadata.Source(),
			Dest:    c.Metadata.Dest(),
			Network: c.Metadata.Network,
			Chain:   strings.Join(c.Chains, "→"),
		})
	}
	return rows
}

// LatencyMsg extracts the failover group's per-server latency + selection from
// the Clash API /proxies map. The group is named "proxy"; with a single server
// "proxy" is the server itself. ok is false when no "proxy" entry is present.
func LatencyMsg(proxies map[string]clashapi.ProxyState) (notify.LatencyMsg, bool) {
	group, present := proxies["proxy"]
	if !present {
		return notify.LatencyMsg{}, false
	}
	var msg notify.LatencyMsg
	if len(group.All) > 0 { // urltest group (multi-server)
		msg.Selected = group.Now
		for _, tag := range group.All {
			msg.Rows = append(msg.Rows, notify.LatencyRow{
				Tag:      tag,
				Delay:    proxies[tag].LastDelay(),
				Selected: tag == group.Now,
			})
		}
	} else { // single server
		msg.Selected = "proxy"
		msg.Rows = []notify.LatencyRow{{Tag: "proxy", Delay: group.LastDelay(), Selected: true}}
	}
	return msg, true
}
