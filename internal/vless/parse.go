package vless

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ParseLink parses a vless:// share link into a ServerProfile. It only validates
// structure; it does not contact the network or verify keys.
//
// Form: vless://<uuid>@<host>:<port>?<params>#<name>
// Recognised params: type, security, pbk, sid, sni, fp, flow, alpn, host, path,
// serviceName, headerType, allowInsecure. Defaults: type=tcp, security=none.
func ParseLink(raw string) (ServerProfile, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return ServerProfile{}, parseErr("link", raw, ErrNotVLESS)
	}
	if !strings.EqualFold(u.Scheme, "vless") {
		return ServerProfile{}, parseErr("scheme", u.Scheme, ErrNotVLESS)
	}

	// UUID lives in the userinfo.
	if u.User == nil || u.User.Username() == "" {
		return ServerProfile{}, parseErr("uuid", "", ErrMissingUUID)
	}
	uuid := u.User.Username()
	if !uuidRe.MatchString(uuid) {
		return ServerProfile{}, parseErr("uuid", uuid, ErrInvalidUUID)
	}

	host := u.Hostname()
	if host == "" {
		return ServerProfile{}, parseErr("host", "", ErrMissingHost)
	}
	portStr := u.Port()
	if portStr == "" {
		return ServerProfile{}, parseErr("port", "", ErrMissingPort)
	}
	port64, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port64 == 0 {
		return ServerProfile{}, parseErr("port", portStr, ErrInvalidPort)
	}

	q := u.Query()

	security := SecurityType(strings.ToLower(strings.TrimSpace(q.Get("security"))))
	if security == "" {
		security = SecurityNone
	}
	switch security {
	case SecurityNone, SecurityTLS, SecurityReality:
	default:
		return ServerProfile{}, parseErr("security", string(security), ErrUnsupportedSecurity)
	}

	transport := TransportType(strings.ToLower(strings.TrimSpace(q.Get("type"))))
	if transport == "" {
		transport = TransportTCP
	}
	switch transport {
	case TransportTCP, TransportGRPC, TransportWS, TransportHTTP:
	default:
		return ServerProfile{}, parseErr("type", string(transport), ErrUnsupportedTransport)
	}

	// VLESS classic links only carry encryption=none; reject anything else
	// (e.g. the new post-quantum mlkem encryption) rather than silently
	// downgrading to a plain connection the server would reject.
	if enc := strings.ToLower(strings.TrimSpace(q.Get("encryption"))); enc != "" && enc != "none" {
		return ServerProfile{}, parseErr("encryption", enc, ErrUnsupportedEncryption)
	}

	p := ServerProfile{
		UUID:     uuid,
		Host:     host,
		Port:     uint16(port64),
		Name:     u.Fragment,
		Flow:     strings.TrimSpace(q.Get("flow")),
		Security: security,
		Raw:      raw,
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
			return ServerProfile{}, parseErr("pbk", "", ErrMissingRealityKey)
		}
		p.Reality = RealityParams{Enabled: true, PublicKey: pbk, ShortID: q.Get("sid")}
	}

	return p, nil
}

// ParseLinks parses one or more vless:// links into a ProfileSet, preserving
// order as failover priority. Each input string may itself contain several
// links separated by newlines, spaces, semicolons, or commas (so a pasted blob
// or a repeated --key flag both work). It fails on the first malformed link.
func ParseLinks(raws []string) (ProfileSet, error) {
	var set ProfileSet
	for _, raw := range raws {
		for _, link := range splitLinks(raw) {
			p, err := ParseLink(link)
			if err != nil {
				return ProfileSet{}, err
			}
			set.Profiles = append(set.Profiles, p)
		}
	}
	if len(set.Profiles) == 0 {
		return ProfileSet{}, ErrNoLinks
	}
	return set, nil
}

// splitLinks breaks a blob into individual link tokens on any whitespace,
// semicolon, or comma. Commas inside a single link's query (e.g. alpn=h2,http/1.1)
// are not a concern because real links contain no top-level comma — vless links
// always start with the "vless://" scheme, which we additionally guard on.
func splitLinks(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\t' || r == ' ' || r == ';'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
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
