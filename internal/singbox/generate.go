package singbox

import (
	"net"

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

// GenerateProxyConfig builds the persistent PROXY instance on the default
// ports (socks 1080 + http 2080). See GenerateProxyConfigPorts.
func GenerateProxyConfig(p vless.ServerProfile, physIface string) (Config, error) {
	return GenerateProxyConfigPorts(p, physIface, DefaultPorts())
}

// GenerateProxyConfigPorts builds the persistent PROXY instance. physIface is
// non-empty ONLY in VPN mode (decision D3): then bind_interface/
// default_interface pin egress to the physical NIC so it escapes our own TUN.
// In proxy-only mode physIface == "" and those fields are omitted, so traffic
// follows the default route (through Cisco if active — D4: bypass is
// impossible). The PROXY instance never contains a tun inbound.
func GenerateProxyConfigPorts(p vless.ServerProfile, physIface string, ports Ports) (Config, error) {
	ports = ports.withDefaults()
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

	vlessOut := VLESSOutbound{
		Type:           "vless",
		Tag:            proxyTag,
		Server:         p.Host,
		ServerPort:     int(p.Port),
		UUID:           p.UUID,
		Flow:           p.Flow,
		ConnectTimeout: connectTimout,
		TLS:            tlsCfg,
		Transport:      transport,
		BindInterface:  physIface,
	}

	cfg := Config{
		Log: ptrLog(),
		DNS: &DNS{
			Servers: []DNSServer{
				{Type: "https", Tag: proxyDNSTag, Server: "1.1.1.1", Detour: proxyTag},
				{Type: "local", Tag: localDNSTag},
			},
			Final:    proxyDNSTag,
			Strategy: "ipv4_only",
		},
		Inbounds: []any{
			SocksInbound{Type: "socks", Tag: socksTag, Listen: listenAddr, ListenPort: ports.Socks},
			HTTPInbound{Type: "http", Tag: httpTag, Listen: listenAddr, ListenPort: ports.HTTP},
		},
		Outbounds: []any{
			vlessOut,
			DirectOutbound{Type: "direct", Tag: directTag, BindInterface: physIface},
		},
		Route: &Route{
			DefaultInterface: physIface,
			Rules: []RouteRule{
				{Action: "sniff", Timeout: "3s"},
				{IPIsPrivate: true, Outbound: directTag},
				{DomainRegex: ruDomainRegex, Outbound: directTag},
			},
			Final:                 proxyTag,
			DefaultDomainResolver: localDNSTag,
		},
		Experimental: &Experimental{CacheFile: &CacheFile{Enabled: false}},
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
	return GenerateForwarderConfigPorts(p, DefaultPorts())
}

// GenerateForwarderConfigPorts is GenerateForwarderConfig with a custom proxy
// socks port (the forwarder must dial wherever the proxy actually listens).
func GenerateForwarderConfigPorts(p vless.ServerProfile, ports Ports) (Config, error) {
	ports = ports.withDefaults()
	rules := []RouteRule{
		{Action: "sniff", Timeout: "3s"},
		{Inbound: []string{tunTag}, Protocol: "dns", Action: "hijack-dns"},
		{IPIsPrivate: true, Outbound: directTag},
	}
	if ip := net.ParseIP(p.Host); ip != nil {
		cidr := p.Host + "/32"
		if ip.To4() == nil {
			cidr = p.Host + "/128"
		}
		rules = append(rules, RouteRule{IPCIDR: []string{cidr}, Outbound: directTag})
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
