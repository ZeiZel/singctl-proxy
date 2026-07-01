package singbox

import (
	"fmt"
	"net"
	"net/url"

	"singctl/internal/vless"
)

// Fixed policy/server constants mirroring the existing config.json semantics.
const (
	socksTag      = "socks-in"
	httpTag       = "http-in"
	tunTag        = "tun-in"
	proxyTag      = "proxy"
	directTag     = "direct"
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
}

// GenerateProxyConfig builds the persistent PROXY instance on the default
// ports (socks 1080 + http 2080). See GenerateProxyConfigOpts.
func GenerateProxyConfig(p vless.ServerProfile, physIface string) (Config, error) {
	return GenerateProxyConfigOpts(vless.SingleSet(p), ProxyOpts{PhysIface: physIface})
}

// GenerateProxyConfigPorts is the single-profile adapter with custom ports.
func GenerateProxyConfigPorts(p vless.ServerProfile, physIface string, ports Ports) (Config, error) {
	return GenerateProxyConfigOpts(vless.SingleSet(p), ProxyOpts{PhysIface: physIface, Ports: ports})
}

// vlessOutbound builds one VLESS outbound for profile p under the given tag.
// physIface (VPN mode) binds its egress to the physical NIC.
func vlessOutbound(p vless.ServerProfile, tag, physIface string) VLESSOutbound {
	var tlsCfg *TLS
	if p.Security == vless.SecurityTLS || p.Security == vless.SecurityReality {
		tlsCfg = &TLS{Enabled: true, ServerName: p.TLS.ServerName, Insecure: p.TLS.Insecure}
		if len(p.TLS.ALPN) > 0 {
			tlsCfg.ALPN = p.TLS.ALPN
		}
		if p.TLS.Fingerprint != "" {
			tlsCfg.UTLS = &UTLS{Enabled: true, Fingerprint: p.TLS.Fingerprint}
		}
		if p.Reality.Enabled {
			tlsCfg.Reality = &Reality{Enabled: true, PublicKey: p.Reality.PublicKey, ShortID: p.Reality.ShortID}
		}
	}

	var transport *Transport
	switch p.Transport.Type {
	case vless.TransportGRPC:
		transport = &Transport{Type: "grpc", ServiceName: p.Transport.ServiceName}
	case vless.TransportWS:
		transport = &Transport{Type: "ws", Path: p.Transport.Path}
		if len(p.Transport.Host) > 0 {
			transport.Headers = map[string]string{"Host": p.Transport.Host[0]}
		}
	case vless.TransportHTTP:
		transport = &Transport{Type: "http", Path: p.Transport.Path}
		if len(p.Transport.Host) > 0 {
			transport.Host = p.Transport.Host
		}
	case vless.TransportTCP:
		transport = nil
	}

	return VLESSOutbound{
		Type:           "vless",
		Tag:            tag,
		Server:         p.Host,
		ServerPort:     int(p.Port),
		UUID:           p.UUID,
		Flow:           p.Flow,
		ConnectTimeout: connectTimout,
		TLS:            tlsCfg,
		Transport:      transport,
		BindInterface:  physIface,
	}
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
// or more servers. PhysIface is non-empty ONLY in VPN mode (decision D3): then
// bind_interface/default_interface pin egress to the physical NIC so it escapes
// our own TUN. In proxy-only mode PhysIface == "" and those fields are omitted,
// so traffic follows the default route (through Cisco if active — D4). The PROXY
// instance never contains a tun inbound.
//
// With one server the VLESS outbound is tagged "proxy" directly (byte-identical
// to the historical config). With several, each server is tagged "proxy-0..N"
// and a "proxy" urltest group latency-tests them and routes through the fastest
// reachable one — so route.final ("proxy") and the DNS detour are unchanged.
func GenerateProxyConfigOpts(set vless.ProfileSet, opts ProxyOpts) (Config, error) {
	ports := opts.Ports.withDefaults()

	var outbounds []any
	// bootHosts collects the VLESS server hostnames (when addressed by domain)
	// and, in multi-server mode, the urltest probe host. These must be resolved
	// by the bootstrap resolver (public DoH via "direct"), NOT by the system/
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
	if set.Multi() {
		tags := make([]string, set.Len())
		for i, p := range set.Profiles {
			tag := proxyServerTag(i)
			tags[i] = tag
			outbounds = append(outbounds, vlessOutbound(p, tag, opts.PhysIface))
			if net.ParseIP(p.Host) == nil {
				bootHosts = append(bootHosts, p.Host)
			}
		}
		ut := opts.URLTest.withDefaults()
		outbounds = append(outbounds, URLTestOutbound{
			Type: "urltest", Tag: proxyTag, Outbounds: tags,
			URL: ut.URL, Interval: ut.Interval, Tolerance: ut.Tolerance,
		})
		bootHosts = append(bootHosts, probeHost(ut.URL))
	} else {
		p := set.Primary()
		outbounds = append(outbounds, vlessOutbound(p, proxyTag, opts.PhysIface))
		if net.ParseIP(p.Host) == nil {
			bootHosts = append(bootHosts, p.Host)
		}
	}
	outbounds = append(outbounds, DirectOutbound{Type: "direct", Tag: directTag, BindInterface: opts.PhysIface})

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

	rules := []RouteRule{
		{Action: "sniff", Timeout: "3s"},
		{IPIsPrivate: true, Outbound: directTag},
		{DomainRegex: ruDomainRegex, Outbound: directTag},
	}

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

// GenerateForwarderConfig builds the on-demand TUN-FORWARDER instance used in
// VPN mode. It owns the system default route (auto_route) and relays everything
// to the persistent proxy at 127.0.0.1:1080, where the proxy does the ru/private
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
//   - If the server host is a literal IP, a belt-and-suspenders ip_cidr→direct
//     rule prevents any loop even if the proxy's bind were ineffective (R1).
func GenerateForwarderConfig(p vless.ServerProfile) (Config, error) {
	return GenerateForwarderConfigSet(vless.SingleSet(p), DefaultPorts())
}

// GenerateForwarderConfigPorts is the single-profile adapter with a custom port.
func GenerateForwarderConfigPorts(p vless.ServerProfile, ports Ports) (Config, error) {
	return GenerateForwarderConfigSet(vless.SingleSet(p), ports)
}

// GenerateForwarderConfigSet builds the on-demand TUN forwarder for a set of
// servers. The proxy socks port is where the forwarder relays everything; each
// server host that is a literal IP gets a belt-and-suspenders ip_cidr→direct
// loop-guard (so traffic to any server never re-enters our TUN).
func GenerateForwarderConfigSet(set vless.ProfileSet, ports Ports) (Config, error) {
	ports = ports.withDefaults()
	rules := []RouteRule{
		{Action: "sniff", Timeout: "3s"},
		{Inbound: []string{tunTag}, Protocol: "dns", Action: "hijack-dns"},
		{IPIsPrivate: true, Outbound: directTag},
	}
	for _, p := range set.Profiles {
		if ip := net.ParseIP(p.Host); ip != nil {
			cidr := p.Host + "/32"
			if ip.To4() == nil {
				cidr = p.Host + "/128"
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
