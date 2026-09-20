// Package vless implements the VLESS protocol module: it parses vless://
// share links and renders the matching sing-box outbound.
//
// VLESS classic links carry encryption=none; anything else (e.g. the newer
// post-quantum mlkem encryption) is rejected outright rather than silently
// downgraded to a connection the server would refuse.
//
// The xhttp transport is Xray's own (formerly SplitHTTP); upstream sing-box
// has no native support for it, so this module renders it as singctl's own
// "vless-xhttp" outbound type instead of a plain VLESSOutbound.
package vless

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

// Sentinel errors returned (wrapped in *ParseError) by Parse. Callers match
// them with errors.Is.
var (
	ErrNotVLESS              = errors.New("not a vless:// link")
	ErrMissingUUID           = errors.New("missing UUID")
	ErrInvalidUUID           = errors.New("invalid UUID")
	ErrMissingHost           = errors.New("missing host")
	ErrMissingPort           = errors.New("missing port")
	ErrInvalidPort           = errors.New("invalid port")
	ErrMissingRealityKey     = errors.New("reality selected but public key (pbk) is missing")
	ErrUnsupportedTransport  = errors.New("unsupported transport type")
	ErrUnsupportedSecurity   = errors.New("unsupported security type")
	ErrUnsupportedEncryption = errors.New("unsupported encryption (only 'none' is supported)")
	ErrUnsupportedXHTTPMode  = errors.New("unsupported xhttp mode")
	ErrInvalidXHTTPExtra     = errors.New("invalid xhttp extra (must be valid JSON)")
)

// ParseError annotates a sentinel error with the offending field/value.
type ParseError struct {
	Field string
	Value string
	Err   error
}

func (e *ParseError) Error() string {
	if e.Value != "" {
		return fmt.Sprintf("vless: %s=%q: %v", e.Field, e.Value, e.Err)
	}
	return fmt.Sprintf("vless: %s: %v", e.Field, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

func parseErr(field, value string, err error) *ParseError {
	return &ParseError{Field: field, Value: value, Err: err}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// SecurityType is the transport security layer of a VLESS outbound.
type SecurityType string

const (
	SecurityNone    SecurityType = "none"
	SecurityTLS     SecurityType = "tls"
	SecurityReality SecurityType = "reality"
)

// TransportType is VLESS's stream transport.
type TransportType string

const (
	TransportTCP  TransportType = "tcp"
	TransportGRPC TransportType = "grpc"
	TransportWS   TransportType = "ws"
	TransportHTTP TransportType = "http"
	// TransportXHTTP is Xray's XHTTP (formerly SplitHTTP) transport. Upstream
	// sing-box has no XHTTP support, so singctl implements it itself.
	TransportXHTTP TransportType = "xhttp"
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
	Path        string   // ws / http / xhttp
	Host        []string // ws / http / xhttp Host header
	HeaderType  string
	Mode        string // xhttp: "", "auto", "packet-up", "stream-up", "stream-one"
	Extra       string // xhttp: the link's raw `extra=` JSON blob (compacted), "" if absent
}

// Params is everything a VLESS outbound needs, carved out of the link.
type Params struct {
	UUID string
	Host string // bare host or IP; IPv6 stored WITHOUT brackets
	Port uint16
	Flow string // xtls-rprx-vision

	Security  SecurityType
	TLS       TLSParams
	Reality   RealityParams
	Transport TransportParams
}

// Module implements protocol.Module, protocol.Relabeler and singbox.Renderer
// for VLESS.
type Module struct{}

// New returns the VLESS protocol module.
func New() Module { return Module{} }

// Descriptor implements protocol.Module.
func (Module) Descriptor() protocol.Descriptor {
	return protocol.Descriptor{
		Name:    "vless",
		Title:   "VLESS",
		Kind:    protocol.KindOutbound,
		Input:   protocol.InputLink,
		Schemes: []string{"vless"},
	}
}

// Parse parses a vless:// share link. It only validates structure; it does
// not contact the network or verify keys.
//
// Form: vless://<uuid>@<host>:<port>?<params>#<name>
// Recognised params: type, security, pbk, sid, sni, fp, flow, alpn, host, path,
// serviceName, headerType, allowInsecure, mode, extra (the last two only for
// type=xhttp). Defaults: type=tcp, security=none.
func (Module) Parse(raw string) (protocol.Profile, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return protocol.Profile{}, parseErr("link", raw, ErrNotVLESS)
	}
	if !strings.EqualFold(u.Scheme, "vless") {
		return protocol.Profile{}, parseErr("scheme", u.Scheme, ErrNotVLESS)
	}

	// UUID lives in the userinfo.
	if u.User == nil || u.User.Username() == "" {
		return protocol.Profile{}, parseErr("uuid", "", ErrMissingUUID)
	}
	uuid := u.User.Username()
	if !uuidRe.MatchString(uuid) {
		return protocol.Profile{}, parseErr("uuid", uuid, ErrInvalidUUID)
	}

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
		security = SecurityNone
	}
	switch security {
	case SecurityNone, SecurityTLS, SecurityReality:
	default:
		return protocol.Profile{}, parseErr("security", string(security), ErrUnsupportedSecurity)
	}

	transport := TransportType(strings.ToLower(strings.TrimSpace(q.Get("type"))))
	if transport == "" {
		transport = TransportTCP
	}
	// "splithttp" is the legacy name for XHTTP (Xray renamed it); normalize so
	// downstream code only ever sees TransportXHTTP.
	if transport == "splithttp" {
		transport = TransportXHTTP
	}
	switch transport {
	case TransportTCP, TransportGRPC, TransportWS, TransportHTTP, TransportXHTTP:
	default:
		return protocol.Profile{}, parseErr("type", string(transport), ErrUnsupportedTransport)
	}

	// VLESS classic links only carry encryption=none; reject anything else
	// (e.g. the new post-quantum mlkem encryption) rather than silently
	// downgrading to a plain connection the server would reject.
	if enc := strings.ToLower(strings.TrimSpace(q.Get("encryption"))); enc != "" && enc != "none" {
		return protocol.Profile{}, parseErr("encryption", enc, ErrUnsupportedEncryption)
	}

	params := Params{
		UUID:     uuid,
		Host:     host,
		Port:     uint16(port64),
		Flow:     strings.TrimSpace(q.Get("flow")),
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

	// mode/extra are xhttp-only; a non-xhttp link carrying a stray mode= must
	// not be rejected, so only parse/validate them when the transport is xhttp.
	if transport == TransportXHTTP {
		mode := strings.ToLower(strings.TrimSpace(q.Get("mode")))
		switch mode {
		case "", "auto", "packet-up", "stream-up", "stream-one":
		default:
			return protocol.Profile{}, parseErr("mode", mode, ErrUnsupportedXHTTPMode)
		}
		params.Transport.Mode = mode

		if extra := q.Get("extra"); extra != "" {
			if !json.Valid([]byte(extra)) {
				return protocol.Profile{}, parseErr("extra", extra, ErrInvalidXHTTPExtra)
			}
			var buf bytes.Buffer
			if err := json.Compact(&buf, []byte(extra)); err != nil {
				return protocol.Profile{}, parseErr("extra", extra, ErrInvalidXHTTPExtra)
			}
			params.Transport.Extra = buf.String()
		}
	}

	return protocol.Profile{
		Protocol: "vless",
		Label:    u.Fragment,
		Raw:      raw,
		Params:   params,
	}, nil
}

// SetLabel implements protocol.Relabeler: it returns the link with its
// #fragment (display name) replaced, preserving everything else.
func (Module) SetLabel(raw, label string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", parseErr("link", raw, ErrNotVLESS)
	}
	u.Fragment = strings.TrimSpace(label)
	return u.String(), nil
}

// xhttpOutboundType is singctl's own outbound type name — sing-box has no
// XHTTP transport, so it is registered by internal/singboxext into the
// embedded core's outbound registry.
const xhttpOutboundType = "vless-xhttp"

// RenderNode implements singbox.Renderer: an XHTTPOutbound when the transport
// is xhttp, otherwise a plain VLESSOutbound.
func (Module) RenderNode(p protocol.Profile, o singbox.RenderOpts) (any, error) {
	params, ok := p.Params.(Params)
	if !ok {
		return nil, fmt.Errorf("vless: unexpected Params type %T", p.Params)
	}
	if params.Transport.Type == TransportXHTTP {
		return xhttpOutbound(params, o), nil
	}
	return singbox.VLESSOutbound{
		Type:           "vless",
		Tag:            o.Tag,
		Server:         params.Host,
		ServerPort:     int(params.Port),
		UUID:           params.UUID,
		Flow:           params.Flow,
		ConnectTimeout: o.ConnectTimeout,
		TLS:            tlsCfg(params.Security, params.TLS, params.Reality),
		Transport:      buildTransport(params.Transport),
		BindInterface:  o.BindInterface,
	}, nil
}

// xhttpOutbound builds singctl's own "vless-xhttp" outbound for p.
func xhttpOutbound(p Params, o singbox.RenderOpts) singbox.XHTTPOutbound {
	var host string
	if len(p.Transport.Host) > 0 {
		host = p.Transport.Host[0]
	}
	var extra json.RawMessage
	if p.Transport.Extra != "" {
		extra = json.RawMessage(p.Transport.Extra)
	}
	return singbox.XHTTPOutbound{
		Type:           xhttpOutboundType,
		Tag:            o.Tag,
		Server:         p.Host,
		ServerPort:     int(p.Port),
		UUID:           p.UUID,
		ConnectTimeout: o.ConnectTimeout,
		TLS:            tlsCfg(p.Security, p.TLS, p.Reality),
		// Flow (XTLS Vision) is deliberately not emitted: it is a raw-TCP-only
		// feature and Xray itself rejects it with non-raw transports like xhttp.
		XHTTP: &singbox.XHTTP{
			Path:  p.Transport.Path,
			Host:  host,
			Mode:  p.Transport.Mode,
			Extra: extra,
		},
		BindInterface: o.BindInterface,
	}
}

// tlsCfg builds the TLS block shared by both VLESS outbound shapes: nil when
// security is "none", otherwise carrying utls/reality as applicable.
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

// buildTransport builds the shared stream-transport block from Transport
// params. TCP (and anything unrecognised) carries no transport block.
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
