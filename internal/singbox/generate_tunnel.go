package singbox

import (
	"net"

	"singctl/internal/vless"
)

// TunnelOpts configures GenerateTunnelConfigSet, the single-instance sandboxed
// VPN config used by the App Store SKU's NEPacketTunnelProvider appex.
//
// Unlike the Developer-ID daemon — which splits a PROXY instance and a
// TUN-FORWARDER instance so the forwarder can bind its own dialer to the
// physical NIC (see GenerateProxyConfigOpts / GenerateForwarderConfigSet) —
// sing-box inside the sandboxed appex runs as a SINGLE instance: the tun
// inbound and the VLESS outbound(s) live in the same config, and there is no
// physical-interface bind at all. The App Sandbox does not allow binding a
// socket to a specific NIC, and it doesn't need to: the appex only ever sees
// what NEPacketTunnelFlow hands it, so there is no risk of dialing back into
// our own TUN the way there would be for a process riding the default route.
//
// The zero value uses the historical urltest defaults (see withDefaults).
type TunnelOpts struct {
	URLTest URLTestParams // failover group settings (used only with >1 server)
}

func (o TunnelOpts) withDefaults() TunnelOpts {
	o.URLTest = o.URLTest.withDefaults()
	return o
}

// GenerateTunnelConfigSet builds the single-instance sandboxed VPN config for
// the App Store SKU from a set of one or more VLESS servers.
//
//   - Tun inbound: same tunTag/tunAddress/auto_route/strict_route/stack shape
//     as GenerateForwarderConfigSet's inbound — it owns the system default
//     route.
//   - Outbounds: identical to GenerateProxyConfigOpts (single VLESS outbound
//     tagged "proxy", or several "proxy-0..N" outbounds behind a "proxy"
//     urltest failover group), EXCEPT no bind_interface anywhere — physIface
//     is not a concept here, so vlessOutbound/DirectOutbound are always
//     called with "".
//   - DNS: DoH 1.1.1.1 detoured through the proxy tag (Final), plus the same
//     multi-server bootstrap-DNS fix as GenerateProxyConfigOpts: VLESS server
//     hostnames and (in multi-server mode) the urltest probe host resolve via
//     a second, detour-less DoH server instead of the one that detours
//     through the not-yet-healthy proxy/urltest outbound — otherwise the
//     group can never bootstrap its own health check. Since this is a single
//     instance, tun-hijacked DNS queries are answered by this exact DNS
//     module (no second instance to round-trip through, unlike the
//     Developer-ID forwarder, which relays hijacked DNS to the persistent
//     PROXY instance over its own DoH+detour).
//   - Route rules: sniff; tun-inbound DNS queries → hijack-dns; ip_is_private
//     → direct (LAN short-circuit); a belt-and-suspenders ip_cidr → direct
//     loop-guard per literal-IP server host (mirrors
//     GenerateForwarderConfigSet). route.final is the proxy/urltest tag, so
//     everything else rides the tunnel. auto_detect_interface is set so the
//     instance's own dialer (direct + the DoH detour) stays off its own tun.
//   - No socks inbound, no Clash API, no experimental section: none of that
//     is reachable or useful inside the sandboxed appex.
func GenerateTunnelConfigSet(set vless.ProfileSet, opts TunnelOpts) (Config, error) {
	opts = opts.withDefaults()

	var outbounds []any
	// bootHosts: the same bootstrap-loop problem GenerateProxyConfigOpts
	// solves (see its comment) — VLESS server hostnames and, in multi-server
	// mode, the urltest probe host must resolve via a detour-less bootstrap
	// DoH server, never via the DoH server that detours through the
	// not-yet-healthy proxy/urltest outbound, and never via any
	// corporate/system resolver.
	var bootHosts []string
	if set.Multi() {
		tags := make([]string, set.Len())
		for i, p := range set.Profiles {
			tag := proxyServerTag(i)
			tags[i] = tag
			outbounds = append(outbounds, vlessOutbound(p, tag, ""))
			if net.ParseIP(p.Host) == nil {
				bootHosts = append(bootHosts, p.Host)
			}
		}
		outbounds = append(outbounds, URLTestOutbound{
			Type: "urltest", Tag: proxyTag, Outbounds: tags,
			URL: opts.URLTest.URL, Interval: opts.URLTest.Interval, Tolerance: opts.URLTest.Tolerance,
		})
		bootHosts = append(bootHosts, probeHost(opts.URLTest.URL))
	} else {
		p := set.Primary()
		outbounds = append(outbounds, vlessOutbound(p, proxyTag, ""))
		if net.ParseIP(p.Host) == nil {
			bootHosts = append(bootHosts, p.Host)
		}
	}
	outbounds = append(outbounds, DirectOutbound{Type: "direct", Tag: directTag})

	// Bootstrap DNS: resolve bootHosts via DoH 1.1.1.1 with no detour (dials
	// out directly, since sing-box's default dial IS direct) so the server/
	// probe hostnames never depend on the proxy outbound or a corporate
	// resolver. See GenerateProxyConfigOpts for the full rationale.
	var dnsRules []DNSRule
	dnsServers := []DNSServer{
		{Type: "https", Tag: proxyDNSTag, Server: "1.1.1.1", Detour: proxyTag},
		{Type: "local", Tag: localDNSTag},
	}
	if hosts := dedupeHosts(bootHosts); len(hosts) > 0 {
		dnsServers = append(dnsServers, DNSServer{Type: "https", Tag: bootDNSTag, Server: "1.1.1.1"})
		dnsRules = append(dnsRules, DNSRule{Domain: hosts, Server: bootDNSTag})
	}

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
			Servers:  dnsServers,
			Rules:    dnsRules,
			Final:    proxyDNSTag,
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
		Outbounds: outbounds,
		Route: &Route{
			AutoDetectInterface:   true,
			Rules:                 rules,
			Final:                 proxyTag,
			DefaultDomainResolver: localDNSTag,
		},
	}
	return cfg, nil
}
