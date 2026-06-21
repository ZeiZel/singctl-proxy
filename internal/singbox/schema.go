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
	Log          *Log          `json:"log,omitempty"`
	DNS          *DNS          `json:"dns,omitempty"`
	Inbounds     []any         `json:"inbounds,omitempty"`
	Outbounds    []any         `json:"outbounds,omitempty"`
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
	Final    string      `json:"final,omitempty"`
	Strategy string      `json:"strategy,omitempty"`
}

type DNSServer struct {
	Type   string `json:"type"` // "https", "local", ...
	Tag    string `json:"tag"`
	Server string `json:"server,omitempty"`
	Detour string `json:"detour,omitempty"`
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

type DirectOutbound struct {
	Type          string `json:"type"` // "direct"
	Tag           string `json:"tag"`
	BindInterface string `json:"bind_interface,omitempty"`
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
	IPCIDR      []string `json:"ip_cidr,omitempty"`
	ProcessName []string `json:"process_name,omitempty"`
	ProcessPath []string `json:"process_path,omitempty"`
	Outbound    string   `json:"outbound,omitempty"`
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
