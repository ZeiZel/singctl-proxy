// Package vless parses vless:// share links into a neutral ServerProfile.
// It performs only structural validation (no network, no crypto, no I/O), so it
// is fully unit-testable as a pure function.
package vless

// SecurityType is the transport security layer of a VLESS outbound.
type SecurityType string

const (
	SecurityNone    SecurityType = "none"
	SecurityTLS     SecurityType = "tls"
	SecurityReality SecurityType = "reality"
)

// TransportType is the stream transport of a VLESS outbound.
type TransportType string

const (
	TransportTCP  TransportType = "tcp"
	TransportGRPC TransportType = "grpc"
	TransportWS   TransportType = "ws"
	TransportHTTP TransportType = "http"
)

// TLSParams holds TLS-layer settings parsed from the link.
type TLSParams struct {
	ServerName  string
	Fingerprint string // utls fingerprint, e.g. "chrome"
	ALPN        []string
	Insecure    bool
}

// RealityParams holds REALITY settings. Enabled is true only when
// Security == SecurityReality.
type RealityParams struct {
	Enabled   bool
	PublicKey string
	ShortID   string
}

// TransportParams holds stream-transport settings.
type TransportParams struct {
	Type        TransportType
	ServiceName string   // grpc
	Path        string   // ws / http
	Host        []string // ws / http Host header
	HeaderType  string
}

// ProfileSet is an ordered list of VLESS endpoints. The order is the failover
// priority: index 0 is the primary (prior), the rest are fallbacks (subprior).
// When it holds more than one profile, the generated config latency-tests them
// (a sing-box urltest group) and routes through the fastest reachable one.
type ProfileSet struct {
	Profiles []ServerProfile
}

// Len reports how many servers are in the set.
func (s ProfileSet) Len() int { return len(s.Profiles) }

// Primary returns the highest-priority profile. It panics on an empty set;
// callers build sets via ParseLinks, which rejects empties.
func (s ProfileSet) Primary() ServerProfile { return s.Profiles[0] }

// Multi reports whether the set needs a failover group (more than one server).
func (s ProfileSet) Multi() bool { return len(s.Profiles) > 1 }

// SingleSet wraps one profile as a ProfileSet (adapter for single-key callers).
func SingleSet(p ServerProfile) ProfileSet { return ProfileSet{Profiles: []ServerProfile{p}} }

// ServerProfile is the neutral, transport-agnostic representation of a VLESS
// endpoint. Both sing-box configs (proxy + tun-forwarder) are derived from it.
type ServerProfile struct {
	UUID      string
	Host      string // bare host or IP; IPv6 stored WITHOUT brackets
	Port      uint16
	Name      string // from the URL fragment, percent-decoded
	Flow      string
	Security  SecurityType
	TLS       TLSParams
	Reality   RealityParams
	Transport TransportParams
	Raw       string // original link, for display/debug
}
