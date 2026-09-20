// Package tuic owns the whole vertical slice for tuic (v5) share links:
// parsing tuic:// into Params, relabeling, and rendering the sing-box
// outbound. Nothing outside this package knows the shape of Params.
package tuic

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
const Name protocol.Name = "tuic"

// uuidRe validates a canonical (hyphenated) UUID.
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Sentinel errors specific to tuic:// links.
var (
	ErrNotTUIC                      = errors.New("not a tuic:// link")
	ErrMissingUUID                  = errors.New("missing UUID")
	ErrInvalidUUID                  = errors.New("invalid UUID")
	ErrMissingHost                  = errors.New("missing host")
	ErrMissingPort                  = errors.New("missing port")
	ErrInvalidPort                  = errors.New("invalid port")
	ErrMissingPassword              = errors.New("missing password")
	ErrUnsupportedCongestionControl = errors.New("unsupported congestion_control (valid: cubic, new_reno, bbr)")
	ErrUnsupportedUDPRelayMode      = errors.New("unsupported udp_relay_mode (valid: native, quic)")
)

// TLS holds TLS-layer settings parsed from the link. TUIC is TLS-only by
// protocol definition, so a Params value always renders with TLS enabled.
type TLS struct {
	ServerName string
	ALPN       []string
	Insecure   bool
}

// Params holds everything tuic (v5) needs, carved out of the old fat
// link.ServerProfile — tuic carries "uuid:password" in the userinfo and
// validates congestion control and UDP relay mode.
type Params struct {
	Host     string // bare host or IP; IPv6 stored WITHOUT brackets
	Port     uint16
	UUID     string
	Password string

	TLS TLS

	CongestionControl string // cubic | new_reno | bbr
	UDPRelayMode      string // native | quic
	ZeroRTTHandshake  bool
}

// Module implements protocol.Module, protocol.Relabeler and singbox.Renderer
// for tuic (v5) share links.
type Module struct{}

// New returns the tuic module.
func New() Module { return Module{} }

// Descriptor declares tuic's identity.
func (Module) Descriptor() protocol.Descriptor {
	return protocol.Descriptor{
		Name:    Name,
		Title:   "TUIC",
		Kind:    protocol.KindOutbound,
		Input:   protocol.InputLink,
		Schemes: []string{"tuic"},
	}
}

// Parse parses a tuic:// (v5) share link. It only validates structure; it
// does not contact the network or verify keys.
//
// Form: tuic://<uuid>:<password>@<host>:<port>?<params>#<name>
// The userinfo carries both credentials: username is the UUID, password is
// everything after the colon. Recognised params: congestion_control
// (congestion alias), udp_relay_mode, zero_rtt_handshake (reduce_rtt alias),
// sni, alpn, allow_insecure (insecure/allowInsecure aliases).
// TUIC runs over QUIC and is TLS-only by definition: there is no stream
// transport to configure.
func (Module) Parse(raw string) (protocol.Profile, error) {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil {
		return protocol.Profile{}, parseErr("link", trimmed, ErrNotTUIC)
	}
	if !strings.EqualFold(u.Scheme, "tuic") {
		return protocol.Profile{}, parseErr("scheme", u.Scheme, ErrNotTUIC)
	}

	if u.User == nil || u.User.Username() == "" {
		return protocol.Profile{}, parseErr("uuid", "", ErrMissingUUID)
	}
	uuid := u.User.Username()
	if !uuidRe.MatchString(uuid) {
		return protocol.Profile{}, parseErr("uuid", uuid, ErrInvalidUUID)
	}
	password, ok := u.User.Password()
	if !ok || password == "" {
		return protocol.Profile{}, parseErr("password", "", ErrMissingPassword)
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

	congestion := strings.ToLower(strings.TrimSpace(firstNonEmpty(q.Get("congestion_control"), q.Get("congestion"))))
	switch congestion {
	case "", "cubic", "new_reno", "bbr":
	default:
		return protocol.Profile{}, parseErr("congestion_control", congestion, ErrUnsupportedCongestionControl)
	}

	udpRelayMode := strings.ToLower(strings.TrimSpace(q.Get("udp_relay_mode")))
	switch udpRelayMode {
	case "", "native", "quic":
	default:
		return protocol.Profile{}, parseErr("udp_relay_mode", udpRelayMode, ErrUnsupportedUDPRelayMode)
	}

	zeroRTT := firstNonEmpty(q.Get("zero_rtt_handshake"), q.Get("reduce_rtt"))
	insecure := firstNonEmpty(q.Get("allow_insecure"), q.Get("insecure"), q.Get("allowInsecure"))

	return protocol.Profile{
		Protocol: Name,
		Label:    u.Fragment,
		Raw:      trimmed,
		Params: Params{
			Host:     host,
			Port:     uint16(port64),
			UUID:     uuid,
			Password: password,
			TLS: TLS{
				ServerName: q.Get("sni"),
				ALPN:       splitCSV(q.Get("alpn")),
				Insecure:   insecure == "1" || strings.EqualFold(insecure, "true"),
			},
			CongestionControl: congestion,
			UDPRelayMode:      udpRelayMode,
			ZeroRTTHandshake:  zeroRTT == "1" || strings.EqualFold(zeroRTT, "true"),
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
		return "", parseErr("link", trimmed, ErrNotTUIC)
	}
	u.Fragment = strings.TrimSpace(label)
	return u.String(), nil
}

// RenderNode builds this profile's sing-box outbound. TUIC is TLS-only by
// protocol definition, so TLS is always populated.
func (Module) RenderNode(p protocol.Profile, o singbox.RenderOpts) (any, error) {
	params, ok := p.Params.(Params)
	if !ok {
		return nil, fmt.Errorf("tuic: RenderNode got Params of type %T, want tuic.Params", p.Params)
	}
	return singbox.TUICOutbound{
		Type:              "tuic",
		Tag:               o.Tag,
		Server:            params.Host,
		ServerPort:        int(params.Port),
		UUID:              params.UUID,
		Password:          params.Password,
		CongestionControl: params.CongestionControl,
		UDPRelayMode:      params.UDPRelayMode,
		ZeroRTTHandshake:  params.ZeroRTTHandshake,
		ConnectTimeout:    o.ConnectTimeout,
		TLS:               tlsCfg(params.TLS),
		BindInterface:     o.BindInterface,
	}, nil
}

// tlsCfg builds the sing-box TLS block. TUIC is TLS-only by protocol
// definition, so it is always enabled.
func tlsCfg(t TLS) *singbox.TLS {
	tls := &singbox.TLS{Enabled: true, ServerName: t.ServerName, Insecure: t.Insecure}
	if len(t.ALPN) > 0 {
		tls.ALPN = t.ALPN
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
		return fmt.Sprintf("tuic: %s=%q: %v", e.Field, e.Value, e.Err)
	}
	return fmt.Sprintf("tuic: %s: %v", e.Field, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

func parseErr(field, value string, err error) *ParseError {
	return &ParseError{Field: field, Value: value, Err: err}
}
