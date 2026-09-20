// Package hysteria owns the whole vertical slice for hysteria (v1) share
// links: parsing hysteria:// (and its hy:// alias) into Params, relabeling,
// and rendering the sing-box outbound. Nothing outside this package knows the
// shape of Params.
package hysteria

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
const Name protocol.Name = "hysteria"

// Sentinel errors specific to hysteria:// (v1) links.
var (
	ErrNotHysteria         = errors.New("not a hysteria:// link")
	ErrMissingHost         = errors.New("missing host")
	ErrMissingPort         = errors.New("missing port")
	ErrInvalidPort         = errors.New("invalid port")
	ErrMissingAuth         = errors.New("missing auth")
	ErrMissingHysteriaMbps = errors.New("hysteria v1 needs both upmbps and downmbps (its congestion control is rate-based, not loss-based)")
)

// bandwidthRe extracts the leading integer of a bandwidth value that may carry
// a unit suffix, e.g. "100", "100 mbps", "100mbps" all yield 100.
var bandwidthRe = regexp.MustCompile(`^\s*(\d+)`)

// TLS holds TLS-layer settings parsed from the link. Hysteria v1 is TLS-only
// by protocol definition, so a Params value always renders with TLS enabled.
type TLS struct {
	ServerName string
	ALPN       []string
	Insecure   bool
}

// Params holds everything hysteria (v1) needs, carved out of the old fat
// link.ServerProfile — hysteria v1's credential lives in the "auth" query
// parameter rather than the userinfo, and its up/down bandwidth hints are
// mandatory.
type Params struct {
	Host string // bare host or IP; IPv6 stored WITHOUT brackets
	Port uint16

	TLS TLS

	// UpMbps/DownMbps are mandatory: v1's congestion control is rate-based,
	// not loss-based.
	UpMbps   int
	DownMbps int
	Obfs     string
	AuthStr  string
}

// Module implements protocol.Module, protocol.Relabeler and singbox.Renderer
// for hysteria (v1) share links.
type Module struct{}

// New returns the hysteria module.
func New() Module { return Module{} }

// Descriptor declares hysteria's identity. Both "hysteria" and its "hy" alias
// are claimed here; the registry routes them to this module.
func (Module) Descriptor() protocol.Descriptor {
	return protocol.Descriptor{
		Name:    Name,
		Title:   "Hysteria",
		Kind:    protocol.KindOutbound,
		Input:   protocol.InputLink,
		Schemes: []string{"hysteria", "hy"},
	}
}

// Parse parses a hysteria:// (v1, "hy://" alias) share link. It only
// validates structure; it does not contact the network or verify keys.
//
// Form: hysteria://<host>:<port>?<params>#<name>
// Unlike hysteria2, the credential is NOT in the userinfo — it is the "auth"
// query parameter. Recognised params: auth (required), upmbps/up,
// downmbps/down (both effectively mandatory: v1's congestion control is
// rate-based), obfs, peer/sni, alpn, insecure.
// Hysteria v1 runs over QUIC and is TLS-only by definition: there is no
// stream transport to configure.
func (Module) Parse(raw string) (protocol.Profile, error) {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil {
		return protocol.Profile{}, parseErr("link", trimmed, ErrNotHysteria)
	}
	if !strings.EqualFold(u.Scheme, "hysteria") && !strings.EqualFold(u.Scheme, "hy") {
		return protocol.Profile{}, parseErr("scheme", u.Scheme, ErrNotHysteria)
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

	auth := q.Get("auth")
	if auth == "" {
		return protocol.Profile{}, parseErr("auth", "", ErrMissingAuth)
	}

	upMbps, err := parseMbps(firstNonEmpty(q.Get("upmbps"), q.Get("up")))
	if err != nil {
		return protocol.Profile{}, parseErr("upmbps", q.Get("upmbps"), err)
	}
	downMbps, err := parseMbps(firstNonEmpty(q.Get("downmbps"), q.Get("down")))
	if err != nil {
		return protocol.Profile{}, parseErr("downmbps", q.Get("downmbps"), err)
	}
	if upMbps == 0 || downMbps == 0 {
		return protocol.Profile{}, parseErr("upmbps/downmbps", "", ErrMissingHysteriaMbps)
	}

	return protocol.Profile{
		Protocol: Name,
		Label:    u.Fragment,
		Raw:      trimmed,
		Params: Params{
			Host: host,
			Port: uint16(port64),
			TLS: TLS{
				ServerName: firstNonEmpty(q.Get("peer"), q.Get("sni")),
				ALPN:       splitCSV(q.Get("alpn")),
				Insecure:   q.Get("insecure") == "1" || strings.EqualFold(q.Get("insecure"), "true"),
			},
			UpMbps:   upMbps,
			DownMbps: downMbps,
			Obfs:     q.Get("obfs"),
			AuthStr:  auth,
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
		return "", parseErr("link", trimmed, ErrNotHysteria)
	}
	u.Fragment = strings.TrimSpace(label)
	return u.String(), nil
}

// RenderNode builds this profile's sing-box outbound. Hysteria v1 is
// TLS-only by protocol definition, so TLS is always populated.
func (Module) RenderNode(p protocol.Profile, o singbox.RenderOpts) (any, error) {
	params, ok := p.Params.(Params)
	if !ok {
		return nil, fmt.Errorf("hysteria: RenderNode got Params of type %T, want hysteria.Params", p.Params)
	}
	return singbox.HysteriaOutbound{
		Type:           "hysteria",
		Tag:            o.Tag,
		Server:         params.Host,
		ServerPort:     int(params.Port),
		UpMbps:         params.UpMbps,
		DownMbps:       params.DownMbps,
		Obfs:           params.Obfs,
		AuthStr:        params.AuthStr,
		ConnectTimeout: o.ConnectTimeout,
		TLS:            tlsCfg(params.TLS),
		BindInterface:  o.BindInterface,
	}, nil
}

// tlsCfg builds the sing-box TLS block. Hysteria is TLS-only by protocol
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
		return fmt.Sprintf("hysteria: %s=%q: %v", e.Field, e.Value, e.Err)
	}
	return fmt.Sprintf("hysteria: %s: %v", e.Field, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

func parseErr(field, value string, err error) *ParseError {
	return &ParseError{Field: field, Value: value, Err: err}
}
