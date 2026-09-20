package singbox

// WireGuardEndpoint is sing-box's wireguard endpoint. Unlike every other
// protocol singctl speaks, WireGuard is not an outbound: sing-box models it as
// an *endpoint*, declared in the top-level "endpoints" array, a sibling of
// "outbounds" (see docs/protocol-modules.md). adapter.Endpoint embeds
// adapter.Outbound, so Tag is usable anywhere an outbound tag is — a
// WireGuard endpoint can join the urltest failover group and be route.final
// like any outbound.
//
// sing-box gates WireGuard behind the `with_wireguard` build tag
// (include/wireguard.go is `//go:build with_wireguard`, stubbed otherwise).
// Without that tag the endpoint decodes and then fails to construct at
// runtime — the same silent-dead-key failure a missing `with_quic` already
// cost us once — so SINGBOX_TAGS and LIBBOX_TAGS in the Makefile must always
// carry it.
type WireGuardEndpoint struct {
	Type   string `json:"type"` // "wireguard"
	Tag    string `json:"tag"`
	System bool   `json:"system,omitempty"`
	Name   string `json:"name,omitempty"`
	MTU    int    `json:"mtu,omitempty"`
	// Address is the interface's own prefixes, e.g. "10.0.0.2/32". Required.
	Address    []string        `json:"address"`
	PrivateKey string          `json:"private_key"`
	ListenPort int             `json:"listen_port,omitempty"`
	Peers      []WireGuardPeer `json:"peers,omitempty"`
	UDPTimeout string          `json:"udp_timeout,omitempty"`
	Workers    int             `json:"workers,omitempty"`

	// The DialerOptions subset the assembly layer sets via RenderOpts.
	BindInterface  string `json:"bind_interface,omitempty"`
	ConnectTimeout string `json:"connect_timeout,omitempty"`
	Detour         string `json:"detour,omitempty"`
}

// WireGuardPeer is one peer of a WireGuard endpoint.
type WireGuardPeer struct {
	// Address/Port are the peer's own reachable endpoint (host:port in the
	// source config); empty when the peer has none (roaming/no Endpoint=).
	Address                     string   `json:"address,omitempty"`
	Port                        int      `json:"port,omitempty"`
	PublicKey                   string   `json:"public_key"`
	PreSharedKey                string   `json:"pre_shared_key,omitempty"`
	AllowedIPs                  []string `json:"allowed_ips,omitempty"`
	PersistentKeepaliveInterval int      `json:"persistent_keepalive_interval,omitempty"`
	Reserved                    []int    `json:"reserved,omitempty"`
}
