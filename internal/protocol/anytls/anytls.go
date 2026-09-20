// Package anytls owns the whole vertical slice for anytls share links:
// parsing anytls:// into Params, relabeling, and rendering the sing-box
// outbound. Nothing outside this package knows the shape of Params.
package anytls

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

// Name is this module's protocol name, used both in its Descriptor and in
// every Profile it produces.
const Name protocol.Name = "anytls"

// Sentinel errors specific to anytls:// links.
var (
	ErrNotAnyTLS       = errors.New("not an anytls:// link")
	ErrMissingHost     = errors.New("missing host")
	ErrMissingPort     = errors.New("missing port")
	ErrInvalidPort     = errors.New("invalid port")
	ErrMissingPassword = errors.New("missing password")
)

// TLS holds TLS-layer settings parsed from the link. AnyTLS is TLS-only by
// protocol definition, so a Params value always renders with TLS enabled.
type TLS struct {
	ServerName  string
	Fingerprint string // utls fingerprint, e.g. "chrome"
	ALPN        []string
	Insecure    bool
}

// Params holds everything anytls needs, carved out of the old fat
// link.ServerProfile.
type Params struct {
	Host     string // bare host or IP; IPv6 stored WITHOUT brackets
	Port     uint16
	Password string

	TLS TLS
}

// Module implements protocol.Module, protocol.Relabeler and singbox.Renderer
// for anytls share links.
type Module struct{}

// New returns the anytls module.
func New() Module { return Module{} }

// Descriptor declares anytls's identity.
func (Module) Descriptor() protocol.Descriptor {
	return protocol.Descriptor{
		Name:    Name,
		Title:   "AnyTLS",
		Kind:    protocol.KindOutbound,
		Input:   protocol.InputLink,
		Schemes: []string{"anytls"},
	}
}

// Parse parses an anytls:// share link. It only validates structure; it does
// not contact the network or verify keys.
//
// Form: anytls://<password>@<host>:<port>?<params>#<name>
// Recognised params: sni, alpn, fp (utls fingerprint), insecure/allowInsecure
// (both spellings accepted; "1" or "true"). AnyTLS is TLS-only by definition,
// so there is no stream transport to parse.
func (Module) Parse(raw string) (protocol.Profile, error) {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil {
		return protocol.Profile{}, parseErr("link", trimmed, ErrNotAnyTLS)
	}
	if !strings.EqualFold(u.Scheme, "anytls") {
		return protocol.Profile{}, parseErr("scheme", u.Scheme, ErrNotAnyTLS)
	}

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
	insecure := q.Get("insecure") == "1" || strings.EqualFold(q.Get("insecure"), "true") ||
		q.Get("allowInsecure") == "1" || strings.EqualFold(q.Get("allowInsecure"), "true")

	return protocol.Profile{
		Protocol: Name,
		Label:    u.Fragment,
		Raw:      trimmed,
		Params: Params{
			Host:     host,
			Port:     uint16(port64),
			Password: password,
			TLS: TLS{
				ServerName:  q.Get("sni"),
				Fingerprint: q.Get("fp"),
				ALPN:        splitCSV(q.Get("alpn")),
				Insecure:    insecure,
			},
		},
	}, nil
}

// SetLabel returns raw with its URL fragment (display label) replaced by
// label, preserving everything else — a rename without touching the
// secret/host/params.
func (Module) SetLabel(raw, label string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", parseErr("link", trimmed, ErrNotAnyTLS)
	}
	u.Fragment = strings.TrimSpace(label)
	return u.String(), nil
}

// RenderNode builds this profile's sing-box outbound. AnyTLS is TLS-only by
// protocol definition, so TLS is always populated.
func (Module) RenderNode(p protocol.Profile, o singbox.RenderOpts) (any, error) {
	params, ok := p.Params.(Params)
	if !ok {
		return nil, fmt.Errorf("anytls: RenderNode got Params of type %T, want anytls.Params", p.Params)
	}
	return singbox.AnyTLSOutbound{
		Type:           "anytls",
		Tag:            o.Tag,
		Server:         params.Host,
		ServerPort:     int(params.Port),
		Password:       params.Password,
		ConnectTimeout: o.ConnectTimeout,
		TLS:            tlsCfg(params.TLS),
		BindInterface:  o.BindInterface,
	}, nil
}

// tlsCfg builds the sing-box TLS block. AnyTLS is TLS-only by protocol
// definition, so it is always enabled.
func tlsCfg(t TLS) *singbox.TLS {
	tls := &singbox.TLS{Enabled: true, ServerName: t.ServerName, Insecure: t.Insecure}
	if len(t.ALPN) > 0 {
		tls.ALPN = t.ALPN
	}
	if t.Fingerprint != "" {
		tls.UTLS = &singbox.UTLS{Enabled: true, Fingerprint: t.Fingerprint}
	}
	return tls
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

// ParseError annotates a sentinel error with the offending field/value.
type ParseError struct {
	Field string
	Value string
	Err   error
}

func (e *ParseError) Error() string {
	if e.Value != "" {
		return fmt.Sprintf("anytls: %s=%q: %v", e.Field, e.Value, e.Err)
	}
	return fmt.Sprintf("anytls: %s: %v", e.Field, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

func parseErr(field, value string, err error) *ParseError {
	return &ParseError{Field: field, Value: value, Err: err}
}
