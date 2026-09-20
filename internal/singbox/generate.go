package singbox

import (
	"fmt"
	"net"
	"net/url"

	"singctl/internal/firewall"
	"singctl/internal/protocol"
)

// Fixed policy/server constants mirroring the existing config.json semantics.
const (
	socksTag = "socks-in"
	httpTag  = "http-in"
	tunTag   = "tun-in"
	proxyTag = "proxy"
	// autoTag is the urltest group. In multi-server mode "proxy" is a SELECTOR
	// whose members are this group plus every individual server, so the user
	// can pin one server by hand while automatic selection stays one click
	// away. Everything downstream (route.final, the DNS detour) keeps
	// referencing "proxy" and is unaffected by which member is active.
	autoTag   = "auto"
	directTag = "direct"
	// blockTag is the "block" outbound firewall rules point at (F6 item 5).
	// Emitted only when at least one rule blocks something — see
	// firewallRouteRules.
	blockTag      = "block"
	socksOutTag   = "socks-out"
	proxyDNSTag   = "proxy-dns"
	fwdDNSTag     = "fwd-dns"
	bootDNSTag    = "boot-dns"
	localDNSTag   = "local"
	listenAddr    = "127.0.0.1"
	socksPort     = 1080
	httpPort      = 2080
	connectTimout = "10s"
)

// ruDomainRegex routes Russian / IDN domains directly (not through the proxy),
// matching the existing config.json.
var ruDomainRegex = []string{
	`^([\w\-\.]+\.)?ru$`,
	`^([\w\-\.]+\.)?xn--p1ai$`,
	`^([\w\-\.]+\.)?xn--p1acf$`,
	`^([\w\-\.]+\.)?xn--80asehdb$`,
	`^([\w\-\.]+\.)?xn--c1avg$`,
	`^([\w\-\.]+\.)?xn--80aswg$`,
	`^([\w\-\.]+\.)?xn--80adxhks$`,
	`^([\w\-\.]+\.)?moscow$`,
	`^([\w\-\.]+\.)?xn--d1acj3b$`,
}

// tunAddress uses 198.18.0.0/30 (decision D7) to avoid colliding with Cisco's
// 172.18/16 pool observed on the target machine.
var tunAddress = []string{"198.18.0.1/30", "fdfe:dcba:9876::1/126"}

func ptrLog() *Log { return &Log{Level: "info", Timestamp: true} }

// processProbePath is an impossible process path used only to make sing-box turn
// on process-search (so /connections reports the source process) without
// changing any real routing decision — the rule can never match.
const processProbePath = "/singctl/__process_probe_never_matches__"

// Default urltest probe settings for the multi-server failover group.
const (
	defaultURLTestURL       = "https://www.gstatic.com/generate_204"
	defaultURLTestInterval  = "3m"
	defaultURLTestTolerance = 50
)

// Ports are the local listen ports of the persistent proxy. The zero value
// means "defaults" (socks 1080, http 2080) — normalize with withDefaults.
type Ports struct {
	Socks int
	HTTP  int
}

// DefaultPorts mirrors the historical fixed ports.
func DefaultPorts() Ports { return Ports{Socks: socksPort, HTTP: httpPort} }

func (p Ports) withDefaults() Ports {
	if p.Socks == 0 {
		p.Socks = socksPort
	}
	if p.HTTP == 0 {
		p.HTTP = httpPort
	}
	return p
}

// URLTestParams configures the multi-server failover group. The zero value means
// the defaults above.
type URLTestParams struct {
	URL       string
	Interval  string
	Tolerance int
}

func (u URLTestParams) withDefaults() URLTestParams {
	if u.URL == "" {
		u.URL = defaultURLTestURL
	}
	if u.Interval == "" {
		u.Interval = defaultURLTestInterval
	}
	if u.Tolerance == 0 {
		u.Tolerance = defaultURLTestTolerance
	}
	return u
}

// ProxyOpts are the knobs for the persistent proxy config. The zero value is the
// historical single-server, no-Clash, proxy-only config.
type ProxyOpts struct {
	PhysIface string        // VPN mode only; "" = proxy-only (no bind, rides default route)
	Ports     Ports         // local listen ports (zero = defaults)
	ClashAPI  *ClashAPI     // non-nil enables the Clash API + process-search logging
	URLTest   URLTestParams // failover group settings (used only with >1 server)
	// Firewall is the persisted rule set from internal/firewall (F6 item 5).
	// A nil/empty slice adds nothing at all to the generated config — no
	// "block" outbound, no extra route rules — which is what keeps every
	// existing golden file byte-identical (docs/v2-spec.md F6's byte-fidelity
	// requirement).
	Firewall []firewall.Rule
}

// firewallRouteRules translates persisted firewall rules into sing-box route
// rules pointing at the "block" (drop) or "direct" (bypass the tunnel)
// outbound, plus the "block" outbound itself when at least one rule actually
// blocks something. It is placed ahead of the private-IP/ru-domain rules (see
// GenerateProxyConfigOpts) so a firewall decision always wins over the
// default routing. A malformed rule (defensively — Executor.FirewallAdd
// already validates before persisting) contributes no rule rather than
// emitting one that would match everything.
func firewallRouteRules(rules []firewall.Rule) (extraOutbounds []any, extraRules []RouteRule) {
	needsBlock := false
	for _, r := range rules {
		rule := RouteRule{}
		switch {
		case r.Domain != "":
			rule.DomainSuffix = []string{r.Domain}
		case r.CIDR != "":
			rule.IPCIDR = []string{r.CIDR}
		case r.Process != "":
			rule.ProcessName = []string{r.Process}
		default:
			continue
		}
		if r.Action == firewall.ActionBlock {
			rule.Outbound = blockTag
			needsBlock = true
		} else {
			rule.Outbound = directTag
		}
		extraRules = append(extraRules, rule)
	}
	if needsBlock {
		extraOutbounds = append(extraOutbounds, BlockOutbound{Type: "block", Tag: blockTag})
	}
	return extraOutbounds, extraRules
}

// renderNode looks up profile p's protocol module in reg, type-asserts it as
// a Renderer (a module that does not implement Renderer is an engine-wiring
// error, named in the returned error), and renders its sing-box node under
// tag. It returns the node together with the module's declared Kind, so the
// caller knows whether to place it in outbounds or endpoints.
func renderNode(reg *protocol.Registry, p protocol.Profile, tag, physIface string) (any, protocol.Kind, error) {
	m, ok := reg.Module(p.Protocol)
	if !ok {
		return nil, 0, fmt.Errorf("singbox: no protocol module registered for %q", p.Protocol)
	}
	renderer, ok := m.(Renderer)
	if !ok {
		return nil, 0, fmt.Errorf("singbox: protocol %q (%s) does not support the sing-box engine",
			p.Protocol, m.Descriptor().Title)
	}
	node, err := renderer.RenderNode(p, RenderOpts{
		Tag:            tag,
		BindInterface:  physIface,
		ConnectTimeout: connectTimout,
	})
	if err != nil {
		return nil, 0, err
	}
	return node, m.Descriptor().Kind, nil
}

// profileHosts renders p (under a throwaway tag, discarding the node) purely
// to learn the dial host(s) it needs — used where a generator needs a
// profile's host without emitting its outbound at all (GenerateForwarderConfigSet,
// which relays everything to the persistent proxy instead of dialing servers
// itself).
func profileHosts(reg *protocol.Registry, p protocol.Profile) ([]string, error) {
	node, _, err := renderNode(reg, p, "probe", "")
	if err != nil {
		return nil, err
	}
	return nodeHosts(node), nil
}

// placeNode appends node to *outbounds or *endpoints according to kind.
func placeNode(outbounds, endpoints *[]any, kind protocol.Kind, node any) {
	if kind == protocol.KindEndpoint {
		*endpoints = append(*endpoints, node)
		return
	}
	*outbounds = append(*outbounds, node)
}

// nonLiteralHosts returns the hosts in hosts that are domain names, not
// literal IPs — the ones that actually need the bootstrap-DNS treatment.
func nonLiteralHosts(hosts []string) []string {
	var out []string
	for _, h := range hosts {
		if h != "" && net.ParseIP(h) == nil {
			out = append(out, h)
		}
	}
	return out
}

// proxyServerTag is the per-server outbound tag in a multi-server set.
func proxyServerTag(i int) string { return fmt.Sprintf("%s-%d", proxyTag, i) }

// probeHost extracts the DNS-resolvable hostname from a urltest probe URL. It
// returns "" for a URL that needs no DNS (parse failure, no host, or a literal
// IP) — in those cases no bootstrap DNS rule is needed.
func probeHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" || net.ParseIP(host) != nil {
		return ""
	}
	return host
}

// dedupeHosts returns hosts with duplicates removed, preserving first-seen
// order (so the generated config stays deterministic).
func dedupeHosts(hosts []string) []string {
	seen := make(map[string]bool, len(hosts))
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// GenerateProxyConfigOpts builds the persistent PROXY instance from a set of one
// or more profiles, rendered through reg (the registry is injected, never a
// package-level singleton — see docs/protocol-modules.md). PhysIface is
// non-empty ONLY in VPN mode (decision D3): then bind_interface/
// default_interface pin egress to the physical NIC so it escapes our own TUN.
// In proxy-only mode PhysIface == "" and those fields are omitted, so traffic
// follows the default route (through Cisco if active — D4). The PROXY
// instance never contains a tun inbound.
//
// With one profile its node is tagged "proxy" directly (byte-identical to the
// historical config). With several, each is tagged "proxy-0..N" and a "proxy"
// urltest group latency-tests them and routes through the fastest reachable
// one — so route.final ("proxy") and the DNS detour are unchanged. A profile
// whose module declares KindEndpoint (WireGuard) lands in the sibling
// "endpoints" array instead of "outbounds"; either way its tag joins the same
// failover group.
func GenerateProxyConfigOpts(reg *protocol.Registry, profiles []protocol.Profile, opts ProxyOpts) (Config, error) {
	if len(profiles) == 0 {
		return Config{}, fmt.Errorf("singbox: at least one profile is required")
	}
	ports := opts.Ports.withDefaults()

	var outbounds []any
	var endpoints []any
	// bootHosts collects the server hostnames (when addressed by domain) and,
	// in multi-server mode, the urltest probe host. These must be resolved by
	// the bootstrap resolver (public DoH via "direct"), NOT by the system/
	// corporate resolver and NOT through the proxy itself:
	//   - Resolving a proxy server's own address through the proxy is a bootstrap
	//     loop (in multi-server mode it lands the dial on the DoH IP 1.1.1.1 and
	//     fails TLS).
	//   - Under a corporate VPN (Cisco split-tunnel) the system resolver is the
	//     corporate DNS (CGNAT 100.64.x), which is unreachable/unresponsive for
	//     public names from our egress → "lookup <server>: i/o timeout".
	// Routing them through "direct" (which rides the physical/default route where
	// plain internet works) sidesteps both.
	var bootHosts []string

	multi := len(profiles) > 1
	tags := make([]string, 0, len(profiles))
	for i, p := range profiles {
		tag := proxyTag
		if multi {
			tag = proxyServerTag(i)
			tags = append(tags, tag)
		}
		node, kind, err := renderNode(reg, p, tag, opts.PhysIface)
		if err != nil {
			return Config{}, err
		}
		placeNode(&outbounds, &endpoints, kind, node)
		bootHosts = append(bootHosts, nonLiteralHosts(nodeHosts(node))...)
	}
	if multi {
		ut := opts.URLTest.withDefaults()
		outbounds = append(outbounds, URLTestOutbound{
			Type: "urltest", Tag: autoTag, Outbounds: tags,
			URL: ut.URL, Interval: ut.Interval, Tolerance: ut.Tolerance,
		})
		// The selector is what "proxy" resolves to. Its default is the urltest
		// group, so behaviour is unchanged until the user picks a server; the
		// Clash API switches the active member live, without rebuilding the
		// config or dropping the running proxy.
		outbounds = append(outbounds, SelectorOutbound{
			Type: "selector", Tag: proxyTag,
			Outbounds: append([]string{autoTag}, tags...),
			Default:   autoTag,
		})
		bootHosts = append(bootHosts, probeHost(ut.URL))
	}
	outbounds = append(outbounds, DirectOutbound{Type: "direct", Tag: directTag, BindInterface: opts.PhysIface})

	// Firewall (F6 item 5): an empty rule set contributes nothing (nil, nil),
	// which is what keeps every existing golden byte-identical.
	fwOutbounds, fwRules := firewallRouteRules(opts.Firewall)
	outbounds = append(outbounds, fwOutbounds...)

	// Bootstrap DNS: resolve bootHosts via DoH 1.1.1.1 over "direct" so the
	// server/probe hostnames never depend on the corporate resolver or the proxy.
	var dnsRules []DNSRule
	dnsServers := []DNSServer{
		{Type: "https", Tag: proxyDNSTag, Server: "1.1.1.1", Detour: proxyTag},
		{Type: "local", Tag: localDNSTag},
	}
	if hosts := dedupeHosts(bootHosts); len(hosts) > 0 {
		// No detour: sing-box dials a detour-less DNS server directly (the default
		// dial IS direct), which is exactly what we want — resolve the server/
		// probe hostnames over the physical/default route, bypassing both the
		// proxy (no loop) and the corporate resolver. NB: `detour: direct` is
		// rejected at start ("detour to an empty direct outbound makes no sense")
		// because direct carries no special config here, so we omit it.
		dnsServers = append(dnsServers, DNSServer{Type: "https", Tag: bootDNSTag, Server: "1.1.1.1"})
		dnsRules = append(dnsRules, DNSRule{Domain: hosts, Server: bootDNSTag})
	}

	rules := make([]RouteRule, 0, 3+len(fwRules))
	rules = append(rules, RouteRule{Action: "sniff", Timeout: "3s"})
	// Firewall rules take priority over the default private-IP/ru-domain
	// routing below: a block must win even for a .ru domain or a LAN address.
	rules = append(rules, fwRules...)
	rules = append(rules,
		RouteRule{IPIsPrivate: true, Outbound: directTag},
		RouteRule{DomainRegex: ruDomainRegex, Outbound: directTag},
	)

	var experimental *Experimental
	if opts.ClashAPI != nil {
		experimental = &Experimental{CacheFile: &CacheFile{Enabled: false}, ClashAPI: opts.ClashAPI}
		// Enable process-search so /connections reports the client process.
		rules = append(rules, RouteRule{ProcessPath: []string{processProbePath}, Outbound: proxyTag})
	} else {
		experimental = &Experimental{CacheFile: &CacheFile{Enabled: false}}
	}

	cfg := Config{
		Log: ptrLog(),
		DNS: &DNS{
			Servers:  dnsServers,
			Rules:    dnsRules,
			Final:    proxyDNSTag,
			Strategy: "ipv4_only",
		},
		Inbounds: []any{
			SocksInbound{Type: "socks", Tag: socksTag, Listen: listenAddr, ListenPort: ports.Socks},
			HTTPInbound{Type: "http", Tag: httpTag, Listen: listenAddr, ListenPort: ports.HTTP},
		},
		Outbounds: outbounds,
		Endpoints: endpoints,
		Route: &Route{
			DefaultInterface:      opts.PhysIface,
			Rules:                 rules,
			Final:                 proxyTag,
			DefaultDomainResolver: localDNSTag,
		},
		Experimental: experimental,
	}
	return cfg, nil
}

// GenerateForwarderConfigSet builds the on-demand TUN-FORWARDER instance used
// in VPN mode, from a set of one or more profiles rendered through reg. It
// owns the system default route (auto_route) and relays everything to the
// persistent proxy at 127.0.0.1:1080, where the proxy does the ru/private
// split via SNI sniff. Key behaviors (resolving PLAN open-q 8/9):
//
//   - DNS is hijacked at the TUN edge ({protocol:dns}→hijack-dns) and answered by
//     the forwarder's OWN dns module, because in sing-box 1.12 hijacked DNS does
//     NOT flow to the proxy instance. That module uses DoH 1.1.1.1 with
//     detour=socks-out, so DNS is resolved over TCP through the proxy/VLESS
//     tunnel — leak-proof and independent of fragile SOCKS5 UDP-associate.
//   - LAN/private traffic is short-circuited locally ({ip_is_private}→direct via
//     the forwarder's own direct outbound) instead of round-tripping through the
//     proxy, preserving mDNS/printers/router access.
//   - auto_detect_interface keeps the forwarder's own dialer (direct + the DoH
//     detour) off its TUN.
//   - Each profile's dial host is resolved (via a throwaway render — the
//     forwarder never emits per-protocol outbounds itself) purely to add a
//     belt-and-suspenders ip_cidr→direct rule when it is a literal IP, so
//     traffic to it can never loop back through our own bind even if the
//     proxy's bind were ineffective (R1).
func GenerateForwarderConfigSet(reg *protocol.Registry, profiles []protocol.Profile, ports Ports) (Config, error) {
	if len(profiles) == 0 {
		return Config{}, fmt.Errorf("singbox: at least one profile is required")
	}
	ports = ports.withDefaults()
	rules := []RouteRule{
		{Action: "sniff", Timeout: "3s"},
		{Inbound: []string{tunTag}, Protocol: "dns", Action: "hijack-dns"},
		{IPIsPrivate: true, Outbound: directTag},
	}
	for _, p := range profiles {
		hosts, err := profileHosts(reg, p)
		if err != nil {
			return Config{}, err
		}
		for _, h := range hosts {
			ip := net.ParseIP(h)
			if ip == nil {
				continue
			}
			cidr := h + "/32"
			if ip.To4() == nil {
				cidr = h + "/128"
			}
			rules = append(rules, RouteRule{IPCIDR: []string{cidr}, Outbound: directTag})
		}
	}

	cfg := Config{
		Log: ptrLog(),
		DNS: &DNS{
			Servers: []DNSServer{
				{Type: "https", Tag: fwdDNSTag, Server: "1.1.1.1", Detour: socksOutTag},
			},
			Final:    fwdDNSTag,
			Strategy: "ipv4_only",
		},
		Inbounds: []any{
			TunInbound{
				Type:        "tun",
				Tag:         tunTag,
				Address:     tunAddress,
				AutoRoute:   true,
				StrictRoute: true,
				Stack:       "system",
			},
		},
		Outbounds: []any{
			SocksOutbound{Type: "socks", Tag: socksOutTag, Server: listenAddr, ServerPort: ports.Socks},
			DirectOutbound{Type: "direct", Tag: directTag},
		},
		Route: &Route{
			AutoDetectInterface: true,
			Rules:               rules,
			Final:               socksOutTag,
		},
	}
	return cfg, nil
}
