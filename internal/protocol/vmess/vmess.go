// Package vmess implements the VMess protocol module: it parses vmess://
// share links and renders the matching sing-box outbound.
//
// Unlike every other scheme this package's cousins handle, vmess:// is not a
// normal URL: the entire payload after "vmess://" is base64 (standard or
// raw/unpadded — both occur in the wild, and some generators also use the
// URL-safe alphabet) of a flat JSON object (the "v2rayN"/"v2rayNG"
// convention that became the de facto standard). Its display name lives
// inside that JSON's "ps" field rather than a URL fragment, which is why
// SetLabel re-encodes the whole payload instead of touching a fragment.
package vmess

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

// Sentinel errors returned (wrapped in *ParseError) by Parse. Callers match
// them with errors.Is.
var (
	ErrNotVMess             = errors.New("not a vmess:// link")
	ErrInvalidVMessBase64   = errors.New("invalid vmess base64 payload")
	ErrInvalidVMessJSON     = errors.New("invalid vmess JSON payload")
	ErrInvalidVMessAlterID  = errors.New("invalid vmess alterId (aid)")
	ErrMissingHost          = errors.New("missing host")
	ErrMissingPort          = errors.New("missing port")
	ErrInvalidPort          = errors.New("invalid port")
	ErrInvalidUUID          = errors.New("invalid UUID")
	ErrUnsupportedTransport = errors.New("unsupported transport type")
)

// ParseError annotates a sentinel error with the offending field/value.
type ParseError struct {
	Field string
	Value string
	Err   error
}

func (e *ParseError) Error() string {
	if e.Value != "" {
		return fmt.Sprintf("vmess: %s=%q: %v", e.Field, e.Value, e.Err)
	}
	return fmt.Sprintf("vmess: %s: %v", e.Field, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

func parseErr(field, value string, err error) *ParseError {
	return &ParseError{Field: field, Value: value, Err: err}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// SecurityType is the TLS-layer security of a VMess outbound. Unlike VLESS
// and trojan, VMess never carries REALITY.
type SecurityType string

const (
	SecurityNone SecurityType = "none"
	SecurityTLS  SecurityType = "tls"
)

// TransportType is VMess's stream transport.
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

// TransportParams holds stream-transport settings.
type TransportParams struct {
	Type        TransportType
	ServiceName string   // grpc
	Path        string   // ws / http
	Host        []string // ws / http Host header
	HeaderType  string
}

// Params is everything a VMess outbound needs, carved out of the link.
type Params struct {
	UUID string
	Host string // bare host or IP; IPv6 stored WITHOUT brackets
	Port uint16

	Security  SecurityType
	TLS       TLSParams
	Transport TransportParams

	AlterID    int    // legacy MD5 AEAD; 0 for anything modern
	Encryption string // "scy": auto | aes-128-gcm | chacha20-poly1305 | none | zero
}

// vmessJSON mirrors the fields found in the base64-encoded JSON payload of a
// vmess:// link. port and aid are typed as json.RawMessage because different
// generators emit them as either a JSON number or a JSON string.
type vmessJSON struct {
	V    string          `json:"v"`
	PS   string          `json:"ps"`
	Add  string          `json:"add"`
	Port json.RawMessage `json:"port"`
	ID   string          `json:"id"`
	Aid  json.RawMessage `json:"aid"`
	Scy  string          `json:"scy"`
	Net  string          `json:"net"`
	Type string          `json:"type"`
	Host string          `json:"host"`
	Path string          `json:"path"`
	TLS  string          `json:"tls"`
	SNI  string          `json:"sni"`
	ALPN string          `json:"alpn"`
	FP   string          `json:"fp"`
}

// Module implements protocol.Module, protocol.Relabeler and singbox.Renderer
// for VMess.
type Module struct{}

// New returns the VMess protocol module.
func New() Module { return Module{} }

// Descriptor implements protocol.Module.
func (Module) Descriptor() protocol.Descriptor {
	return protocol.Descriptor{
		Name:    "vmess",
		Title:   "VMess",
		Kind:    protocol.KindOutbound,
		Input:   protocol.InputLink,
		Schemes: []string{"vmess"},
	}
}

// Parse parses a vmess:// share link. It only validates structure; it does
// not contact the network or verify keys.
func (Module) Parse(raw string) (protocol.Profile, error) {
	trimmed := strings.TrimSpace(raw)
	const scheme = "vmess://"
	if !strings.HasPrefix(strings.ToLower(trimmed), scheme) {
		return protocol.Profile{}, parseErr("scheme", trimmed, ErrNotVMess)
	}
	payload := trimmed[len(scheme):]

	decoded, _, err := decodeVMessBase64(payload)
	if err != nil {
		return protocol.Profile{}, parseErr("base64", payload, ErrInvalidVMessBase64)
	}

	var vj vmessJSON
	if err := json.Unmarshal(decoded, &vj); err != nil {
		return protocol.Profile{}, parseErr("json", string(decoded), ErrInvalidVMessJSON)
	}

	if vj.Add == "" {
		return protocol.Profile{}, parseErr("add", "", ErrMissingHost)
	}
	if !uuidRe.MatchString(vj.ID) {
		return protocol.Profile{}, parseErr("id", vj.ID, ErrInvalidUUID)
	}

	port, err := vmessNumOrString(vj.Port)
	if err != nil || port == "" {
		return protocol.Profile{}, parseErr("port", string(vj.Port), ErrMissingPort)
	}
	port64, err := strconv.ParseUint(port, 10, 16)
	if err != nil || port64 == 0 {
		return protocol.Profile{}, parseErr("port", port, ErrInvalidPort)
	}

	alterID := 0
	if aid, err := vmessNumOrString(vj.Aid); err == nil && aid != "" {
		n, err := strconv.Atoi(aid)
		if err != nil {
			return protocol.Profile{}, parseErr("aid", aid, ErrInvalidVMessAlterID)
		}
		alterID = n
	}

	encryption := firstNonEmpty(strings.ToLower(strings.TrimSpace(vj.Scy)), "auto")

	transport := TransportType(strings.ToLower(strings.TrimSpace(vj.Net)))
	if transport == "" {
		transport = TransportTCP
	}
	switch transport {
	case TransportTCP, TransportGRPC, TransportWS:
		// recognised as-is
	case "h2":
		// vmess links use "h2" where the neutral model calls it TransportHTTP.
		transport = TransportHTTP
	default:
		return protocol.Profile{}, parseErr("net", string(transport), ErrUnsupportedTransport)
	}

	tp := TransportParams{
		Type:       transport,
		Host:       splitCSV(vj.Host),
		HeaderType: vj.Type,
	}
	if transport == TransportGRPC {
		tp.ServiceName = vj.Path
	} else {
		tp.Path = vj.Path
	}

	sec := SecurityNone
	if strings.EqualFold(strings.TrimSpace(vj.TLS), "tls") {
		sec = SecurityTLS
	}

	params := Params{
		UUID:     vj.ID,
		Host:     vj.Add,
		Port:     uint16(port64),
		Security: sec,
		TLS: TLSParams{
			ServerName:  vj.SNI,
			Fingerprint: vj.FP,
			ALPN:        splitCSV(vj.ALPN),
		},
		Transport:  tp,
		AlterID:    alterID,
		Encryption: encryption,
	}

	return protocol.Profile{
		Protocol: "vmess",
		Label:    vj.PS,
		Raw:      raw,
		Params:   params,
	}, nil
}

// SetLabel implements protocol.Relabeler: it returns the vmess:// link with
// its "ps" (display name) field replaced, re-encoded in the same base64
// flavour it arrived in. Unknown fields (custom keys some panels add) survive
// the round-trip untouched; only "ps" is replaced.
func (Module) SetLabel(raw, label string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	const scheme = "vmess://"
	if !strings.HasPrefix(strings.ToLower(trimmed), scheme) {
		return "", parseErr("scheme", trimmed, ErrNotVMess)
	}
	payload := trimmed[len(scheme):]

	decoded, enc, err := decodeVMessBase64(payload)
	if err != nil {
		return "", parseErr("base64", payload, ErrInvalidVMessBase64)
	}

	fields := make(map[string]json.RawMessage)
	if err := json.Unmarshal(decoded, &fields); err != nil {
		return "", parseErr("json", string(decoded), ErrInvalidVMessJSON)
	}

	psJSON, err := json.Marshal(label)
	if err != nil {
		return "", parseErr("name", label, ErrInvalidVMessJSON)
	}
	fields["ps"] = psJSON

	newJSON, err := json.Marshal(fields)
	if err != nil {
		return "", parseErr("json", string(decoded), ErrInvalidVMessJSON)
	}

	return scheme + enc.EncodeToString(newJSON), nil
}

// RenderNode implements singbox.Renderer, producing a VMessOutbound. Flow is
// deliberately never set — it is a VLESS-only (XTLS Vision) concept.
func (Module) RenderNode(p protocol.Profile, o singbox.RenderOpts) (any, error) {
	params, ok := p.Params.(Params)
	if !ok {
		return nil, fmt.Errorf("vmess: unexpected Params type %T", p.Params)
	}
	return singbox.VMessOutbound{
		Type:           "vmess",
		Tag:            o.Tag,
		Server:         params.Host,
		ServerPort:     int(params.Port),
		UUID:           params.UUID,
		Security:       params.Encryption,
		AlterID:        params.AlterID,
		ConnectTimeout: o.ConnectTimeout,
		TLS:            tlsCfg(params.Security, params.TLS),
		Transport:      buildTransport(params.Transport),
		BindInterface:  o.BindInterface,
	}, nil
}

// tlsCfg builds the TLS block: nil when security is "none", otherwise
// carrying utls as applicable. VMess never carries REALITY.
func tlsCfg(sec SecurityType, tp TLSParams) *singbox.TLS {
	if sec != SecurityTLS {
		return nil
	}
	tls := &singbox.TLS{Enabled: true, ServerName: tp.ServerName, Insecure: tp.Insecure}
	if len(tp.ALPN) > 0 {
		tls.ALPN = tp.ALPN
	}
	if tp.Fingerprint != "" {
		tls.UTLS = &singbox.UTLS{Enabled: true, Fingerprint: tp.Fingerprint}
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

// vmessNumOrString extracts a JSON number-or-string field as a plain string.
// An absent field (nil raw message) returns "" with no error.
func vmessNumOrString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s), nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String(), nil
	}
	return "", ErrInvalidVMessJSON
}

// decodeVMessBase64 decodes the vmess:// payload tolerantly: generators emit
// standard (padded) or raw/unpadded base64 interchangeably, and some also use
// the URL-safe alphabet. Try every combination before giving up; the
// encoding that succeeded is returned too, so callers that re-encode (see
// SetLabel) can round-trip in the same base64 flavour the link arrived in.
func decodeVMessBase64(s string) ([]byte, *base64.Encoding, error) {
	s = strings.TrimSpace(s)
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, enc := range encodings {
		b, err := enc.DecodeString(s)
		if err == nil {
			return b, enc, nil
		}
		lastErr = err
	}
	return nil, nil, lastErr
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
