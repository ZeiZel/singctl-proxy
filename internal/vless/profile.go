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
