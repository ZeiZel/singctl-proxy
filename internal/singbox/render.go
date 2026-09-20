package singbox

import "singctl/internal/protocol"

// Renderer is the capability a protocol module implements to run on the
// sing-box engine. It lives here, next to the schema it produces, rather than
// in internal/protocol — so a module that targets a different engine (see the
// AmneziaWG plan in docs/protocol-modules.md) never has to import this package.
//
// RenderNode returns the config node for one server: an outbound struct for a
// KindOutbound protocol, an endpoint struct for a KindEndpoint one. The caller
// places it in the right top-level array based on the module's Descriptor, so
// the module itself does not need to know the difference.
//
// The returned value must marshal to exactly the JSON sing-box expects; the
// structs in schema.go are the contract, and the golden files pin it.
type Renderer interface {
	RenderNode(p protocol.Profile, o RenderOpts) (any, error)
}

// RenderOpts are the cross-cutting settings the assembly layer owns, so no
// module has to rediscover them.
type RenderOpts struct {
	// Tag is the node's name in the config; the failover group and route.final
	// reference it.
	Tag string
	// BindInterface pins egress to the physical NIC. Non-empty in VPN mode
	// only, where the proxy's own traffic must escape our TUN.
	BindInterface string
	// ConnectTimeout is the dial timeout, e.g. "10s". Empty means the engine
	// default.
	ConnectTimeout string
}

// nodeHosts returns the hostname(s) a rendered outbound/endpoint node dials —
// used by the assembly layer (generate.go) for the bootstrap-DNS host
// collection and the literal-IP loop guard. It switches on the concrete
// sing-box schema types THIS package defines, never on a protocol module's
// own Params (which stay opaque outside their owning module, per the module
// contract): every node a Renderer can produce carries its dial target in
// exactly one of the shapes below.
//
// Returns nil for a node type this switch does not (yet) recognise, which
// simply means that node contributes no host: it opts out of bootstrap DNS
// and the loop guard rather than failing the whole config.
func nodeHosts(node any) []string {
	switch n := node.(type) {
	case VLESSOutbound:
		return oneHost(n.Server)
	case VMessOutbound:
		return oneHost(n.Server)
	case TrojanOutbound:
		return oneHost(n.Server)
	case ShadowsocksOutbound:
		return oneHost(n.Server)
	case HysteriaOutbound:
		return oneHost(n.Server)
	case Hysteria2Outbound:
		return oneHost(n.Server)
	case TUICOutbound:
		return oneHost(n.Server)
	case AnyTLSOutbound:
		return oneHost(n.Server)
	case XHTTPOutbound:
		return oneHost(n.Server)
	case WireGuardEndpoint:
		return wireGuardPeerHosts(n.Peers)
	case *WireGuardEndpoint:
		return wireGuardPeerHosts(n.Peers)
	default:
		return nil
	}
}

func oneHost(h string) []string {
	if h == "" {
		return nil
	}
	return []string{h}
}

// wireGuardPeerHosts collects every peer's reachable endpoint host (a peer
// with no Endpoint= in its source config has none — see WireGuardPeer's doc
// comment — and is skipped).
func wireGuardPeerHosts(peers []WireGuardPeer) []string {
	var out []string
	for _, p := range peers {
		if p.Address != "" {
			out = append(out, p.Address)
		}
	}
	return out
}
