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
