// Package types holds value types shared across netstate, policy, monitor and
// ui so they agree on one contract without import cycles.
package types

// NetState is a passive, read-only snapshot of the host's network state,
// produced by netstate.Detector. It never reflects any action we took on Cisco
// (we only observe).
type NetState struct {
	// DefaultRouteIface owns the unscoped default route (e.g. "en0" or, when
	// Cisco is up, "utun4").
	DefaultRouteIface string
	// PhysicalIface is the best-guess physical egress (the hardware interface
	// with an IP gateway), used to bind the proxy in VPN mode.
	PhysicalIface string
	// Tunnels lists tunnel interfaces (utun/ppp/ipsec) currently present.
	Tunnels []TunnelIface
	// CiscoProcessPresent is corroborating evidence only (the daemon runs even
	// when disconnected); it is NOT used as the source of truth.
	CiscoProcessPresent bool
	// CiscoActive is the verdict: a foreign tunnel (not ours) is up and carrying
	// traffic, i.e. Cisco is connected.
	CiscoActive bool
}

// TunnelIface describes one tunnel interface.
type TunnelIface struct {
	Name        string
	HasIPv4     bool
	NoARP       bool // Cisco's utun signature on macOS
	IsOurs      bool // our own sing-box forwarder TUN (198.18.0.x)
	OwnsDefault bool // currently owns the default route
}
