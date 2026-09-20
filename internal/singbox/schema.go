// Package singbox builds sing-box (v1.12.x) configuration as plain Go structs
// that marshal to the exact JSON sing-box expects. It does NOT import sing-box;
// the contract is the JSON form, validated against golden files. Pure, no I/O.
package singbox

import (
	"bytes"
	"encoding/json"
)

// Config is a sing-box top-level config. Inbounds/Outbounds are []any holding
// the typed structs below, so each entry marshals to exactly its type's fields.
type Config struct {
	Log       *Log  `json:"log,omitempty"`
	DNS       *DNS  `json:"dns,omitempty"`
	Inbounds  []any `json:"inbounds,omitempty"`
	Outbounds []any `json:"outbounds,omitempty"`
	// Endpoints is a sibling array of Outbounds for endpoint-kind protocols
	// (WireGuard; see docs/protocol-modules.md). adapter.Endpoint embeds
	// adapter.Outbound in sing-box, so an endpoint's tag is usable anywhere
	// an outbound tag is — it can join the urltest failover group and be
	// route.final like any outbound.
	Endpoints    []any         `json:"endpoints,omitempty"`
	Route        *Route        `json:"route,omitempty"`
	Experimental *Experimental `json:"experimental,omitempty"`
}

type Log struct {
	Level     string `json:"level,omitempty"`
	Timestamp bool   `json:"timestamp,omitempty"`
	Output    string `json:"output,omitempty"` // file path; keeps logs out of the TUI
	Disabled  bool   `json:"disabled,omitempty"`
}

// --- DNS ---

type DNS struct {
	Servers  []DNSServer `json:"servers"`
	Rules    []DNSRule   `json:"rules,omitempty"`
	Final    string      `json:"final,omitempty"`
	Strategy string      `json:"strategy,omitempty"`
}

type DNSServer struct {
	Type   string `json:"type"` // "https", "local", ...
	Tag    string `json:"tag"`
	Server string `json:"server,omitempty"`
	Detour string `json:"detour,omitempty"`
}

// DNSRule routes matching DNS queries to a specific server. We use it in
// multi-server mode to resolve the urltest probe host via the "local" (system)
// resolver instead of the DoH server that detours through the urltest group —
// otherwise the group's health check can never bootstrap (it would need DNS
// that routes back through the not-yet-healthy group).
type DNSRule struct {
	Domain []string `json:"domain,omitempty"`
	Server string   `json:"server,omitempty"`
}

// --- Inbounds ---

type SocksInbound struct {
	Type       string `json:"type"` // "socks"
	Tag        string `json:"tag"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
}

type HTTPInbound struct {
	Type       string `json:"type"` // "http"
	Tag        string `json:"tag"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
}

type TunInbound struct {
	Type        string   `json:"type"` // "tun"
	Tag         string   `json:"tag"`
	Address     []string `json:"address"`
	AutoRoute   bool     `json:"auto_route"`
	StrictRoute bool     `json:"strict_route"`
	Stack       string   `json:"stack"`
}

// --- Outbounds ---

type VLESSOutbound struct {
	Type           string     `json:"type"` // "vless"
	Tag            string     `json:"tag"`
	Server         string     `json:"server"`
	ServerPort     int        `json:"server_port"`
	UUID           string     `json:"uuid"`
	Flow           string     `json:"flow,omitempty"`
	ConnectTimeout string     `json:"connect_timeout,omitempty"`
	TLS            *TLS       `json:"tls,omitempty"`
	Transport      *Transport `json:"transport,omitempty"`
	BindInterface  string     `json:"bind_interface,omitempty"`
}

// Multiplex is sing-box's shared multiplex option block (vmess/trojan/
// shadowsocks). singctl does not currently derive any of this from a parsed
// link, so the field is always nil/omitted; the type exists so the outbound
// structs carry the full upstream contract.
type Multiplex struct {
	Enabled        bool   `json:"enabled,omitempty"`
	Protocol       string `json:"protocol,omitempty"`
	MaxConnections int    `json:"max_connections,omitempty"`
}

// VMessOutbound is sing-box's vmess outbound. Flow is deliberately absent —
// it is a VLESS-only concept.
type VMessOutbound struct {
	Type           string     `json:"type"` // "vmess"
	Tag            string     `json:"tag"`
	Server         string     `json:"server"`
	ServerPort     int        `json:"server_port"`
	UUID           string     `json:"uuid"`
	Security       string     `json:"security,omitempty"`
	AlterID        int        `json:"alter_id,omitempty"`
	Network        string     `json:"network,omitempty"`
	TLS            *TLS       `json:"tls,omitempty"`
	Transport      *Transport `json:"transport,omitempty"`
	Multiplex      *Multiplex `json:"multiplex,omitempty"`
	PacketEncoding string     `json:"packet_encoding,omitempty"`
	ConnectTimeout string     `json:"connect_timeout,omitempty"`
	BindInterface  string     `json:"bind_interface,omitempty"`
}

// TrojanOutbound is sing-box's trojan outbound.
type TrojanOutbound struct {
	Type           string     `json:"type"` // "trojan"
	Tag            string     `json:"tag"`
	Server         string     `json:"server"`
	ServerPort     int        `json:"server_port"`
	Password       string     `json:"password"`
	Network        string     `json:"network,omitempty"`
	TLS            *TLS       `json:"tls,omitempty"`
	Transport      *Transport `json:"transport,omitempty"`
	Multiplex      *Multiplex `json:"multiplex,omitempty"`
	ConnectTimeout string     `json:"connect_timeout,omitempty"`
	BindInterface  string     `json:"bind_interface,omitempty"`
}

// ShadowsocksOutbound is sing-box's shadowsocks outbound. It carries no `tls`
// field at all — shadowsocks encrypts its own stream, there is no separate
// TLS layer to configure.
type ShadowsocksOutbound struct {
	Type           string     `json:"type"` // "shadowsocks"
	Tag            string     `json:"tag"`
	Server         string     `json:"server"`
	ServerPort     int        `json:"server_port"`
	Method         string     `json:"method"`
	Password       string     `json:"password"`
	Plugin         string     `json:"plugin,omitempty"`
	PluginOpts     string     `json:"plugin_opts,omitempty"`
	Network        string     `json:"network,omitempty"`
	UDPOverTCP     any        `json:"udp_over_tcp,omitempty"`
	Multiplex      *Multiplex `json:"multiplex,omitempty"`
	ConnectTimeout string     `json:"connect_timeout,omitempty"`
	BindInterface  string     `json:"bind_interface,omitempty"`
}

// HysteriaOutbound is sing-box's hysteria (v1) outbound. It is TLS-only by
// protocol definition, so callers always populate TLS via tlsCfg.
type HysteriaOutbound struct {
	Type           string `json:"type"` // "hysteria"
	Tag            string `json:"tag"`
	Server         string `json:"server"`
	ServerPort     int    `json:"server_port"`
	UpMbps         int    `json:"up_mbps,omitempty"`
	DownMbps       int    `json:"down_mbps,omitempty"`
	Obfs           string `json:"obfs,omitempty"`
	AuthStr        string `json:"auth_str,omitempty"`
	Network        string `json:"network,omitempty"`
	TLS            *TLS   `json:"tls,omitempty"`
	ConnectTimeout string `json:"connect_timeout,omitempty"`
	BindInterface  string `json:"bind_interface,omitempty"`
}

// Hysteria2Obfs is hysteria2's nested obfuscation block. Omit the whole
// pointer (not just its fields) when Hysteria2Params.ObfsType is empty.
type Hysteria2Obfs struct {
	Type     string `json:"type"`
	Password string `json:"password"`
}

// Hysteria2Outbound is sing-box's hysteria2 outbound. TLS-only by protocol
// definition.
type Hysteria2Outbound struct {
	Type           string         `json:"type"` // "hysteria2"
	Tag            string         `json:"tag"`
	Server         string         `json:"server"`
	ServerPort     int            `json:"server_port"`
	UpMbps         int            `json:"up_mbps,omitempty"`
	DownMbps       int            `json:"down_mbps,omitempty"`
	Obfs           *Hysteria2Obfs `json:"obfs,omitempty"`
	Password       string         `json:"password,omitempty"`
	Network        string         `json:"network,omitempty"`
	TLS            *TLS           `json:"tls,omitempty"`
	BrutalDebug    bool           `json:"brutal_debug,omitempty"`
	ConnectTimeout string         `json:"connect_timeout,omitempty"`
	BindInterface  string         `json:"bind_interface,omitempty"`
}

// TUICOutbound is sing-box's tuic (v5) outbound. TLS-only by protocol
// definition.
type TUICOutbound struct {
	Type              string `json:"type"` // "tuic"
	Tag               string `json:"tag"`
	Server            string `json:"server"`
	ServerPort        int    `json:"server_port"`
	UUID              string `json:"uuid"`
	Password          string `json:"password,omitempty"`
	CongestionControl string `json:"congestion_control,omitempty"`
	UDPRelayMode      string `json:"udp_relay_mode,omitempty"`
	ZeroRTTHandshake  bool   `json:"zero_rtt_handshake,omitempty"`
	Heartbeat         string `json:"heartbeat,omitempty"`
	Network           string `json:"network,omitempty"`
	TLS               *TLS   `json:"tls,omitempty"`
	ConnectTimeout    string `json:"connect_timeout,omitempty"`
	BindInterface     string `json:"bind_interface,omitempty"`
}

// AnyTLSOutbound is sing-box's anytls outbound. TLS-only by protocol
// definition.
type AnyTLSOutbound struct {
	Type                     string `json:"type"` // "anytls"
	Tag                      string `json:"tag"`
	Server                   string `json:"server"`
	ServerPort               int    `json:"server_port"`
	Password                 string `json:"password"`
	TLS                      *TLS   `json:"tls,omitempty"`
	IdleSessionCheckInterval string `json:"idle_session_check_interval,omitempty"`
	IdleSessionTimeout       string `json:"idle_session_timeout,omitempty"`
	MinIdleSession           int    `json:"min_idle_session,omitempty"`
	ConnectTimeout           string `json:"connect_timeout,omitempty"`
	BindInterface            string `json:"bind_interface,omitempty"`
}

// XHTTPOutbound is singctl's OWN outbound type, not an upstream sing-box one —
// sing-box has no XHTTP transport. It is registered under the type name
// "vless-xhttp" by internal/singboxext into the embedded core's outbound
// registry; this struct only describes the JSON shape that registration
// expects.
type XHTTPOutbound struct {
	Type           string `json:"type"` // "vless-xhttp"
	Tag            string `json:"tag"`
	Server         string `json:"server"`
	ServerPort     int    `json:"server_port"`
	UUID           string `json:"uuid"`
	ConnectTimeout string `json:"connect_timeout,omitempty"`
	TLS            *TLS   `json:"tls,omitempty"`
	XHTTP          *XHTTP `json:"xhttp"`
	BindInterface  string `json:"bind_interface,omitempty"`
}

// XHTTP holds the XHTTP stream settings, mirroring Xray's `xhttpSettings`.
type XHTTP struct {
	Path  string          `json:"path,omitempty"`
	Host  string          `json:"host,omitempty"`
	Mode  string          `json:"mode,omitempty"`
	Extra json.RawMessage `json:"extra,omitempty"`
}

type DirectOutbound struct {
	Type          string `json:"type"` // "direct"
	Tag           string `json:"tag"`
	BindInterface string `json:"bind_interface,omitempty"`
}

// BlockOutbound is a sing-box "block" outbound: any connection routed to it is
// dropped. Emitted only when at least one firewall rule blocks something (see
// firewallRouteRules) — an empty firewall rule set must never add this to the
// generated config (docs/v2-spec.md F6's byte-fidelity requirement).
type BlockOutbound struct {
	Type string `json:"type"` // "block"
	Tag  string `json:"tag"`
}

type SocksOutbound struct {
	Type       string `json:"type"` // "socks"
	Tag        string `json:"tag"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
}

// URLTestOutbound is a sing-box selector group that periodically latency-tests
// its members and routes through the fastest reachable one. We use it to fail
// over between several VLESS servers: Outbounds lists the per-server tags in
// priority order, Tag is the group's stable name (route.final points at it).
type URLTestOutbound struct {
	Type        string   `json:"type"` // "urltest"
	Tag         string   `json:"tag"`
	Outbounds   []string `json:"outbounds"`
	URL         string   `json:"url,omitempty"`       // probe URL, e.g. https://www.gstatic.com/generate_204
	Interval    string   `json:"interval,omitempty"`  // re-test interval, e.g. "3m"
	Tolerance   int      `json:"tolerance,omitempty"` // ms hysteresis before switching
	IdleTimeout string   `json:"idle_timeout,omitempty"`
}

// SelectorOutbound is a manual group (kept for completeness / future manual
// server selection from the TUI).
type SelectorOutbound struct {
	Type      string   `json:"type"` // "selector"
	Tag       string   `json:"tag"`
	Outbounds []string `json:"outbounds"`
	Default   string   `json:"default,omitempty"`
}

type TLS struct {
	Enabled    bool     `json:"enabled"`
	ServerName string   `json:"server_name,omitempty"`
	Insecure   bool     `json:"insecure,omitempty"`
	ALPN       []string `json:"alpn,omitempty"`
	UTLS       *UTLS    `json:"utls,omitempty"`
	Reality    *Reality `json:"reality,omitempty"`
}

type UTLS struct {
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint"`
}

type Reality struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key"`
	ShortID   string `json:"short_id,omitempty"`
}

type Transport struct {
	Type        string            `json:"type"` // "grpc", "ws", "http"
	ServiceName string            `json:"service_name,omitempty"`
	Path        string            `json:"path,omitempty"`
	Host        []string          `json:"host,omitempty"`    // http transport Host list
	Headers     map[string]string `json:"headers,omitempty"` // ws Host header etc.
}

// --- Route ---

type Route struct {
	AutoDetectInterface   bool        `json:"auto_detect_interface,omitempty"`
	DefaultInterface      string      `json:"default_interface,omitempty"`
	Rules                 []RouteRule `json:"rules,omitempty"`
	Final                 string      `json:"final,omitempty"`
	DefaultDomainResolver string      `json:"default_domain_resolver,omitempty"`
}

// RouteRule is a wide struct (omitempty) covering exactly the rule shapes we
// generate: sniff, ip_is_private→direct, domain_regex→direct, ip_cidr→direct,
// and the tun-scoped hijack-dns.
type RouteRule struct {
	Action      string   `json:"action,omitempty"`
	Timeout     string   `json:"timeout,omitempty"`
	Inbound     []string `json:"inbound,omitempty"`
	Protocol    string   `json:"protocol,omitempty"`
	IPIsPrivate bool     `json:"ip_is_private,omitempty"`
	DomainRegex []string `json:"domain_regex,omitempty"`
	// DomainSuffix matches a destination domain and its subdomains — used by
	// firewall domain rules (see firewallRouteRules); nothing else generates it.
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	IPCIDR       []string `json:"ip_cidr,omitempty"`
	ProcessName  []string `json:"process_name,omitempty"`
	ProcessPath  []string `json:"process_path,omitempty"`
	Outbound     string   `json:"outbound,omitempty"`
}

// --- Experimental ---

type Experimental struct {
	CacheFile *CacheFile `json:"cache_file,omitempty"`
	ClashAPI  *ClashAPI  `json:"clash_api,omitempty"`
}

type CacheFile struct {
	Enabled bool `json:"enabled"`
}

// ClashAPI enables sing-box's Clash-compatible HTTP API. We bind it to loopback
// with a random secret and poll /connections (source process, destination,
// chain) and /proxies (per-server latency) for the connections view and
// urltest status. ExternalController is "host:port".
type ClashAPI struct {
	ExternalController string `json:"external_controller,omitempty"`
	Secret             string `json:"secret,omitempty"`
	DefaultMode        string `json:"default_mode,omitempty"`
}

// MarshalIndented renders a Config to deterministic, 2-space-indented JSON with
// HTML escaping disabled (so regex chars stay readable). Determinism holds
// because struct field order and slice order are fixed, and the only map
// (transport headers) is marshaled by encoding/json with keys sorted.
func MarshalIndented(c Config) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	// Encoder appends a trailing newline; keep it for clean golden files.
	return buf.Bytes(), nil
}
