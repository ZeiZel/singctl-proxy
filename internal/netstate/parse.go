package netstate

import (
	"bufio"
	"bytes"
	"net"
	"regexp"
	"strings"

	"singctl/internal/types"
)

var (
	ifaceHeaderRe = regexp.MustCompile(`^(\w+): flags=\w+<([^>]*)>`)
	inetRe        = regexp.MustCompile(`^\s+inet (\d{1,3}(?:\.\d{1,3}){3})\b`)
)

// ParseIfconfig parses `ifconfig` output into per-interface state.
func ParseIfconfig(out []byte) []IfaceInfo {
	var ifaces []IfaceInfo
	var cur *IfaceInfo
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := ifaceHeaderRe.FindStringSubmatch(line); m != nil {
			if cur != nil {
				ifaces = append(ifaces, *cur)
			}
			flags := strings.Split(m[2], ",")
			cur = &IfaceInfo{
				Name:         m[1],
				Up:           contains(flags, "UP"),
				PointToPoint: contains(flags, "POINTOPOINT"),
				NoARP:        contains(flags, "NOARP"),
			}
			continue
		}
		if cur == nil {
			continue
		}
		if m := inetRe.FindStringSubmatch(line); m != nil {
			cur.IPv4 = append(cur.IPv4, m[1])
		}
	}
	if cur != nil {
		ifaces = append(ifaces, *cur)
	}
	return ifaces
}

// ParseRouteGetDefault returns the interface named by `route -n get default`
// (the actually-selected default route), or "" if none.
func ParseRouteGetDefault(out []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "interface:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// ParseNetstatDefaults returns the "default" rows from `netstat -rn -f inet`.
func ParseNetstatDefaults(out []byte) []RouteRow {
	var rows []RouteRow
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[0] != "default" {
			continue
		}
		gw := fields[1]
		rows = append(rows, RouteRow{
			Gateway:     gw,
			Iface:       fields[3],
			IsIPGateway: net.ParseIP(gw) != nil,
		})
	}
	return rows
}

// parseCiscoProcs reports whether the process list shows Cisco Secure Client
// components. Corroborating evidence only (the daemon runs even when
// disconnected).
func parseCiscoProcs(out []byte) bool {
	s := strings.ToLower(string(out))
	for _, marker := range []string{"vpnagentd", "cisco secure client", "acsockext", "com.cisco", "anyconnect"} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// isTunnelName reports whether an interface name is a tunnel device.
func isTunnelName(name string) bool {
	for _, p := range []string{"utun", "ppp", "ipsec", "tun"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// classifyTunnels turns parsed interfaces into TunnelIface records, marking ours
// (IPv4 in ourAddrPrefix, e.g. "198.18.0.") and which owns the default route.
func classifyTunnels(ifaces []IfaceInfo, defaultIface, ourAddrPrefix string) []types.TunnelIface {
	var tuns []types.TunnelIface
	for _, in := range ifaces {
		if !isTunnelName(in.Name) {
			continue
		}
		t := types.TunnelIface{
			Name:        in.Name,
			HasIPv4:     len(in.IPv4) > 0,
			NoARP:       in.NoARP,
			OwnsDefault: in.Name == defaultIface,
		}
		for _, ip := range in.IPv4 {
			if ourAddrPrefix != "" && strings.HasPrefix(ip, ourAddrPrefix) {
				t.IsOurs = true
			}
		}
		tuns = append(tuns, t)
	}
	return tuns
}

// pickPhysical returns the physical egress interface: the first default row with
// an IP gateway that is not a tunnel (falls back to any IP-gateway default).
func pickPhysical(rows []RouteRow) string {
	for _, r := range rows {
		if r.IsIPGateway && !isTunnelName(r.Iface) {
			return r.Iface
		}
	}
	for _, r := range rows {
		if r.IsIPGateway {
			return r.Iface
		}
	}
	return ""
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
