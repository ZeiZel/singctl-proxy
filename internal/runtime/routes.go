package runtime

import (
	"strings"

	"singctl/internal/netstate"
)

// OrphanTunDevices returns tunnel-interface names carrying OUR forwarder address
// prefix (e.g. 198.18.0.x), i.e. leftovers from a crashed previous run. It NEVER
// returns a foreign (Cisco) tunnel: only devices with an IPv4 in ourPrefix
// qualify, so Cisco's 172.18.x utun can never match.
func OrphanTunDevices(ifaces []netstate.IfaceInfo, ourPrefix string) []string {
	var out []string
	for _, in := range ifaces {
		if !strings.HasPrefix(in.Name, "utun") {
			continue
		}
		for _, ip := range in.IPv4 {
			if strings.HasPrefix(ip, ourPrefix) {
				out = append(out, in.Name)
				break
			}
		}
	}
	return out
}

// cleanupCommands returns the argv list that removes leaked forwarder TUN
// devices AND their auto_route override routes. It is empty when there is no
// orphan (ourPrefix device) — so we NEVER touch the routing table unless one of
// our own leaked TUNs is present, which keeps Cisco's routes untouched.
//
// The forwarder's TUN uses auto_route, which installs 0.0.0.0/1 + 128.0.0.0/1 to
// override the default route via our TUN. On a clean stop sing-box removes them;
// after a crash (kill -9) they linger and black-hole all traffic even once the
// device is destroyed, so we delete them explicitly.
func cleanupCommands(ifaces []netstate.IfaceInfo, ourPrefix string) [][]string {
	devs := OrphanTunDevices(ifaces, ourPrefix)
	if len(devs) == 0 {
		return nil
	}
	cmds := make([][]string, 0, len(devs)+2)
	for _, dev := range devs {
		cmds = append(cmds, []string{"/sbin/ifconfig", dev, "destroy"})
	}
	cmds = append(cmds,
		[]string{"/sbin/route", "-n", "delete", "-net", "0.0.0.0/1"},
		[]string{"/sbin/route", "-n", "delete", "-net", "128.0.0.0/1"},
	)
	return cmds
}
