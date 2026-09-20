// Package trojan implements the Trojan protocol module: it parses trojan://
// share links and renders the matching sing-box outbound.
//
// Trojan is TLS-only by definition: a link carrying security=none is a
// broken key and is rejected outright rather than silently accepted.
package trojan

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

// Sentinel errors returned (wrapped in *ParseError) by Parse. Callers match
// them with errors.Is.
var (
	ErrNotTrojan            = errors.New("not a trojan:// link")
	ErrTrojanSecurityNone   = errors.New("trojan is TLS-only: security=none is not a valid trojan link")
	ErrMissingPassword      = errors.New("missing password")
	ErrMissingHost          = errors.New("missing host")
	ErrMissingPort          = errors.New("missing port")
	ErrInvalidPort          = errors.New("invalid port")
	ErrMissingRealityKey    = errors.New("reality selected but public key (pbk) is missing")
	ErrUnsupportedTransport = errors.New("unsupported transport type")
	ErrUnsupportedSecurity  = errors.New("unsupported security type")
)

// ParseError annotates a sentinel error with the offending field/value.
type ParseError struct {
	Field string
	Value string
	Err   error
}

func (e *ParseError) Error() string {
	if e.Value != "" {
		return fmt.Sprintf("trojan: %s=%q: %v", e.Field, e.Value, e.Err)
	}
	return fmt.Sprintf("trojan: %s: %v", e.Field, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

func parseErr(field, value string, err error) *ParseError {
	return &ParseError{Field: field, Value: value, Err: err}
}

// SecurityType is the transport security layer of a trojan outbound. Trojan
// is TLS-only by definition, so this is always SecurityTLS or
// SecurityReality — never SecurityNone (Parse rejects that).
type SecurityType string

const (
	SecurityNone    SecurityType = "none"
	SecurityTLS     SecurityType = "tls"
	SecurityReality SecurityType = "reality"
)

// TransportType is trojan's stream transport.
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

// Params is everything a trojan outbound needs, carved out of the link.
type Params struct {
	Password string
	Host     string // bare host or IP; IPv6 stored WITHOUT brackets
	Port     uint16

	// Security is always SecurityTLS or SecurityReality: trojan is TLS-only,
	// and SecurityReality is reported when REALITY is selected (REALITY still
	// terminates a TLS handshake at the edge).
	Security  SecurityType
	TLS       TLSParams
	Reality   RealityParams
	Transport TransportParams
}

// Module implements protocol.Module, protocol.Relabeler and singbox.Renderer
// for Trojan.
type Module struct{}

// New returns the Trojan protocol module.
func New() Module { return Module{} }

// Descriptor implements protocol.Module.
func (Module) Descriptor() protocol.Descriptor {
	return protocol.Descriptor{
		Name:    "trojan",
		Title:   "Trojan",
		Kind:    protocol.KindOutbound,
		Input:   protocol.InputLink,
		Schemes: []string{"trojan"},
	}
}

// Parse parses a trojan:// share link. It only validates structure; it does
// not contact the network or verify keys.
//
// Form: trojan://<password>@<host>:<port>?<params>#<name>
// Recognised params: sni, alpn, fp, allowInsecure, type (tcp/ws/grpc/http),
// host, path, serviceName, headerType, security (tls/reality), pbk, sid.
// Defaults: type=tcp, security=tls.
func (Module) Parse(raw string) (protocol.Profile, error) {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil {
		return protocol.Profile{}, parseErr("link", trimmed, ErrNotTrojan)
	}
	if !strings.EqualFold(u.Scheme, "trojan") {
		return protocol.Profile{}, parseErr("scheme", u.Scheme, ErrNotTrojan)
	}

	// Password lives in the userinfo; u.User.Username() already
	// percent-decodes it.
	if u.User == nil || u.User.Username() == "" {
		return protocol.Profile{}, parseErr("password", "", ErrMissingPassword)
	}
	password := u.User.Username()

	host := u.Hostname()
	if host == "" {
		return protocol.Profile{}, parseErr("host", "", ErrMissingHost)
	}
	portStr := u.Port()
	if portStr == "" {
		return protocol.Profile{}, parseErr("port", "", ErrMissingPort)
	}
	port64, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port64 == 0 {
		return protocol.Profile{}, parseErr("port", portStr, ErrInvalidPort)
	}

	q := u.Query()

	security := SecurityType(strings.ToLower(strings.TrimSpace(q.Get("security"))))
	if security == "" {
		security = SecurityTLS
	}
	switch security {
	case SecurityNone:
		// Some panels emit a broken trojan link with security=none; trojan is
		// TLS-only by definition, so reject rather than silently upgrading.
		return protocol.Profile{}, parseErr("security", string(security), ErrTrojanSecurityNone)
	case SecurityTLS, SecurityReality:
	default:
		return protocol.Profile{}, parseErr("security", string(security), ErrUnsupportedSecurity)
	}

	transport := TransportType(strings.ToLower(strings.TrimSpace(q.Get("type"))))
	if transport == "" {
		transport = TransportTCP
	}
	switch transport {
	case TransportTCP, TransportGRPC, TransportWS, TransportHTTP:
	default:
		return protocol.Profile{}, parseErr("type", string(transport), ErrUnsupportedTransport)
	}

	params := Params{
		Password: password,
		Host:     host,
		Port:     uint16(port64),
		Security: security,
		TLS: TLSParams{
			ServerName:  q.Get("sni"),
			Fingerprint: q.Get("fp"),
			ALPN:        splitCSV(q.Get("alpn")),
			Insecure:    q.Get("allowInsecure") == "1" || strings.EqualFold(q.Get("allowInsecure"), "true"),
		},
		Transport: TransportParams{
			Type:        transport,
			ServiceName: firstNonEmpty(q.Get("serviceName"), q.Get("servicename")),
			Path:        q.Get("path"),
			Host:        splitCSV(q.Get("host")),
			HeaderType:  q.Get("headerType"),
		},
	}

	if security == SecurityReality {
		pbk := q.Get("pbk")
		if pbk == "" {
			return protocol.Profile{}, parseErr("pbk", "", ErrMissingRealityKey)
		}
		params.Reality = RealityParams{Enabled: true, PublicKey: pbk, ShortID: q.Get("sid")}
	}

	return protocol.Profile{
		Protocol: "trojan",
		Label:    u.Fragment,
		Raw:      trimmed,
		Params:   params,
	}, nil
}

// SetLabel implements protocol.Relabeler: it returns the link with its
// #fragment (display name) replaced, preserving everything else.
func (Module) SetLabel(raw, label string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", parseErr("link", trimmed, ErrNotTrojan)
	}
	u.Fragment = strings.TrimSpace(label)
	return u.String(), nil
}

// RenderNode implements singbox.Renderer, producing a TrojanOutbound.
func (Module) RenderNode(p protocol.Profile, o singbox.RenderOpts) (any, error) {
	params, ok := p.Params.(Params)
	if !ok {
		return nil, fmt.Errorf("trojan: unexpected Params type %T", p.Params)
	}
	return singbox.TrojanOutbound{
		Type:           "trojan",
		Tag:            o.Tag,
		Server:         params.Host,
		ServerPort:     int(params.Port),
		Password:       params.Password,
		ConnectTimeout: o.ConnectTimeout,
		TLS:            tlsCfg(params.Security, params.TLS, params.Reality),
		Transport:      buildTransport(params.Transport),
		BindInterface:  o.BindInterface,
	}, nil
}

// tlsCfg builds the TLS block: nil when security is "none" (never actually
// reachable for trojan, since Parse rejects it), otherwise carrying
// utls/reality as applicable.
func tlsCfg(sec SecurityType, tp TLSParams, rp RealityParams) *singbox.TLS {
	if sec != SecurityTLS && sec != SecurityReality {
		return nil
	}
	tls := &singbox.TLS{Enabled: true, ServerName: tp.ServerName, Insecure: tp.Insecure}
	if len(tp.ALPN) > 0 {
		tls.ALPN = tp.ALPN
	}
	if tp.Fingerprint != "" {
		tls.UTLS = &singbox.UTLS{Enabled: true, Fingerprint: tp.Fingerprint}
	}
	if rp.Enabled {
		tls.Reality = &singbox.Reality{Enabled: true, PublicKey: rp.PublicKey, ShortID: rp.ShortID}
	}
	return tls
}

// buildTransport builds the stream-transport block from Transport params.
// TCP (and anything unrecognised) carries no transport block.
func buildTransport(tp TransportParams) *singbox.Transport {
	switch tp.Type {
	case TransportGRPC:
		return &singbox.Transport{Type: "grpc", ServiceName: tp.ServiceName}
	case TransportWS:
		t := &singbox.Transport{Type: "ws", Path: tp.Path}
		if len(tp.Host) > 0 {
			t.Headers = map[string]string{"Host": tp.Host[0]}
		}
		return t
	case TransportHTTP:
		t := &singbox.Transport{Type: "http", Path: tp.Path}
		if len(tp.Host) > 0 {
			t.Host = tp.Host
		}
		return t
	default: // TransportTCP and anything else: no transport block
		return nil
	}
}

// splitCSV splits a comma-separated value, trimming spaces and dropping empties.
func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// firstNonEmpty returns the first non-empty string.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
