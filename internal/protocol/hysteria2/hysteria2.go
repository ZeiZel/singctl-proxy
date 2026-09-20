// Package hysteria2 owns the whole vertical slice for hysteria2 share links:
// parsing hysteria2:// (and its hy2:// alias) into Params, relabeling, and
// rendering the sing-box outbound. Nothing outside this package knows the
// shape of Params.
package hysteria2

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

// Name is this module's protocol name, used both in its Descriptor and in
// every Profile it produces.
const Name protocol.Name = "hysteria2"

// Sentinel errors specific to hysteria2:// links.
var (
	ErrNotHysteria2    = errors.New("not a hysteria2:// link")
	ErrMissingHost     = errors.New("missing host")
	ErrMissingPort     = errors.New("missing port")
	ErrInvalidPort     = errors.New("invalid port")
	ErrMissingPassword = errors.New("missing password")
	ErrUnsupportedObfs = errors.New("unsupported obfs type (only 'salamander' is supported)")
)

// bandwidthRe extracts the leading integer of a bandwidth value that may carry
// a unit suffix, e.g. "100", "100 mbps", "100mbps" all yield 100.
var bandwidthRe = regexp.MustCompile(`^\s*(\d+)`)

// TLS holds TLS-layer settings parsed from the link. Hysteria2 is TLS-only by
// protocol definition, so a Params value always renders with TLS enabled.
type TLS struct {
	ServerName string
	ALPN       []string
	Insecure   bool
}

// Params holds everything hysteria2 needs, carved out of the old fat
// link.ServerProfile — hysteria2 accepts "user:pass" in the userinfo as one
// password and only "salamander" obfuscation.
type Params struct {
	Host     string // bare host or IP; IPv6 stored WITHOUT brackets
	Port     uint16
	Password string

	TLS TLS

	UpMbps       int
	DownMbps     int
	ObfsType     string // "salamander" or empty
	ObfsPassword string
}

// Module implements protocol.Module, protocol.Relabeler and singbox.Renderer
// for hysteria2 share links.
type Module struct{}

// New returns the hysteria2 module.
func New() Module { return Module{} }

// Descriptor declares hysteria2's identity. Both "hysteria2" and its "hy2"
// alias are claimed here; the registry routes them to this module.
func (Module) Descriptor() protocol.Descriptor {
	return protocol.Descriptor{
		Name:    Name,
		Title:   "Hysteria2",
		Kind:    protocol.KindOutbound,
		Input:   protocol.InputLink,
		Schemes: []string{"hysteria2", "hy2"},
	}
}

// Parse parses a hysteria2:// share link (the "hy2://" alias is dispatched
// here too). It only validates structure; it does not contact the network or
// verify keys.
//
// Form: hysteria2://<password>@<host>:<port>?<params>#<name>
// Recognised params: sni/peer, alpn, insecure/allowInsecure, obfs,
// obfs-password, up/upmbps, down/downmbps.
// Hysteria2 runs over QUIC and is TLS-only by definition: there is no stream
// transport to configure.
func (Module) Parse(raw string) (protocol.Profile, error) {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil {
		return protocol.Profile{}, parseErr("link", trimmed, ErrNotHysteria2)
	}
	if !strings.EqualFold(u.Scheme, "hysteria2") && !strings.EqualFold(u.Scheme, "hy2") {
		return protocol.Profile{}, parseErr("scheme", u.Scheme, ErrNotHysteria2)
	}

	// Password lives in the userinfo. Some panels put "user:pass" there
	// instead of a bare password; hysteria2 itself treats the whole userinfo
	// as the password in that case, so when a colon is present we rejoin
	// username and password (both already percent-decoded by url.Parse)
	// rather than only keeping the username half.
	if u.User == nil || u.User.Username() == "" {
		return protocol.Profile{}, parseErr("password", "", ErrMissingPassword)
	}
	password := u.User.Username()
	if pass, ok := u.User.Password(); ok {
		password = password + ":" + pass
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

	obfsType := strings.ToLower(strings.TrimSpace(q.Get("obfs")))
	if obfsType != "" && obfsType != "salamander" {
		return protocol.Profile{}, parseErr("obfs", obfsType, ErrUnsupportedObfs)
	}

	upMbps, err := parseMbps(firstNonEmpty(q.Get("up"), q.Get("upmbps")))
	if err != nil {
		return protocol.Profile{}, parseErr("up", q.Get("up"), err)
	}
	downMbps, err := parseMbps(firstNonEmpty(q.Get("down"), q.Get("downmbps")))
	if err != nil {
		return protocol.Profile{}, parseErr("down", q.Get("down"), err)
	}

	return protocol.Profile{
		Protocol: Name,
		Label:    u.Fragment,
		Raw:      trimmed,
		Params: Params{
			Host:     host,
			Port:     uint16(port64),
			Password: password,
			TLS: TLS{
				ServerName: firstNonEmpty(q.Get("sni"), q.Get("peer")),
				ALPN:       splitCSV(q.Get("alpn")),
				Insecure:   q.Get("insecure") == "1" || strings.EqualFold(q.Get("insecure"), "true") || q.Get("allowInsecure") == "1" || strings.EqualFold(q.Get("allowInsecure"), "true"),
			},
			UpMbps:       upMbps,
			DownMbps:     downMbps,
			ObfsType:     obfsType,
			ObfsPassword: q.Get("obfs-password"),
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
		return "", parseErr("link", trimmed, ErrNotHysteria2)
	}
	u.Fragment = strings.TrimSpace(label)
	return u.String(), nil
}

// RenderNode builds this profile's sing-box outbound. Hysteria2 is TLS-only
// by protocol definition, so TLS is always populated. The nested obfs block
// is omitted entirely (not just its fields) when ObfsType is empty.
func (Module) RenderNode(p protocol.Profile, o singbox.RenderOpts) (any, error) {
	params, ok := p.Params.(Params)
	if !ok {
		return nil, fmt.Errorf("hysteria2: RenderNode got Params of type %T, want hysteria2.Params", p.Params)
	}
	var obfs *singbox.Hysteria2Obfs
	if params.ObfsType != "" {
		obfs = &singbox.Hysteria2Obfs{Type: params.ObfsType, Password: params.ObfsPassword}
	}
	return singbox.Hysteria2Outbound{
		Type:           "hysteria2",
		Tag:            o.Tag,
		Server:         params.Host,
		ServerPort:     int(params.Port),
		UpMbps:         params.UpMbps,
		DownMbps:       params.DownMbps,
		Obfs:           obfs,
		Password:       params.Password,
		ConnectTimeout: o.ConnectTimeout,
		TLS:            tlsCfg(params.TLS),
		BindInterface:  o.BindInterface,
	}, nil
}

// tlsCfg builds the sing-box TLS block. Hysteria2 is TLS-only by protocol
// definition, so it is always enabled.
func tlsCfg(t TLS) *singbox.TLS {
	tls := &singbox.TLS{Enabled: true, ServerName: t.ServerName, Insecure: t.Insecure}
	if len(t.ALPN) > 0 {
		tls.ALPN = t.ALPN
	}
	return tls
}

// parseMbps parses a hysteria bandwidth hint (up/down), tolerating an optional
// "mbps" (or any other) suffix, with or without a separating space. An empty
// string yields 0, nil (the field is simply left unset).
func parseMbps(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	m := bandwidthRe.FindStringSubmatch(s)
	if m == nil {
		return 0, strconv.ErrSyntax
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, err
	}
	return n, nil
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

// ParseError annotates a sentinel error with the offending field/value.
type ParseError struct {
	Field string
	Value string
	Err   error
}

func (e *ParseError) Error() string {
	if e.Value != "" {
		return fmt.Sprintf("hysteria2: %s=%q: %v", e.Field, e.Value, e.Err)
	}
	return fmt.Sprintf("hysteria2: %s: %v", e.Field, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

func parseErr(field, value string, err error) *ParseError {
	return &ParseError{Field: field, Value: value, Err: err}
}
