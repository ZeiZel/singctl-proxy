package netstate

import (
	"context"

	"singctl/internal/types"
)

// OurTunAddrPrefix is the IPv4 prefix of our own forwarder TUN (198.18.0.0/30,
// decision D7). A tunnel carrying such an address is ours, not Cisco.
const OurTunAddrPrefix = "198.18.0."

// Detector produces a passive NetState snapshot from read-only OS commands.
type Detector struct {
	run           CommandRunner
	ourAddrPrefix string
}

// New returns a Detector using the given command runner.
func New(run CommandRunner) *Detector {
	return &Detector{run: run, ourAddrPrefix: OurTunAddrPrefix}
}

// Observe gathers ifconfig / route / netstat / ps and derives a NetState. It is
// strictly read-only and never touches Cisco. A missing default route (route
// command failing) is tolerated — it just yields an empty DefaultRouteIface.
func (d *Detector) Observe(ctx context.Context) (types.NetState, error) {
	ifOut, err := d.run.Run(ctx, "ifconfig")
	if err != nil {
		return types.NetState{}, err
	}
	nsOut, err := d.run.Run(ctx, "netstat", "-rn", "-f", "inet")
	if err != nil {
		return types.NetState{}, err
	}
	// route/ps are best-effort; failures don't abort the snapshot.
	rtOut, _ := d.run.Run(ctx, "route", "-n", "get", "default")
	psOut, _ := d.run.Run(ctx, "ps", "-axo", "pid,comm")

	ifaces := ParseIfconfig(ifOut)
	defIface := ParseRouteGetDefault(rtOut)
	defaults := ParseNetstatDefaults(nsOut)
	tunnels := classifyTunnels(ifaces, defIface, d.ourAddrPrefix)

	ciscoActive := false
	for _, t := range tunnels {
		if t.IsOurs {
			continue
		}
		// A foreign tunnel with an IPv4 that either carries the Cisco NOARP
		// signature or owns the default route = Cisco is connected.
		if t.HasIPv4 && (t.NoARP || t.OwnsDefault) {
			ciscoActive = true
		}
	}

	return types.NetState{
		DefaultRouteIface:   defIface,
		PhysicalIface:       pickPhysical(defaults),
		Tunnels:             tunnels,
		CiscoProcessPresent: parseCiscoProcs(psOut),
		CiscoActive:         ciscoActive,
	}, nil
}
