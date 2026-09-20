// Package shadowsocks implements the Shadowsocks protocol module: it parses
// ss:// share links and renders the matching sing-box outbound.
//
// Two link formats exist in the wild and both are accepted:
//
//   - SIP002 (modern): ss://<base64url(method:password)>@host:port/?plugin=...#name
//     The userinfo is base64 of "method:password" (any of the four common
//     base64 flavors: standard/URL alphabet, padded/unpadded). Some panels
//     percent-encode "method:password" directly instead of base64-encoding
//     it; that is detected two ways: net/url itself splits an unescaped ':'
//     in the userinfo into Username/Password, and a %3A-escaped colon
//     survives base64 decoding as a literal ':' in the decoded userinfo.
//   - Legacy: ss://<base64(method:password@host:port)>#name — the entire
//     span after the scheme (minus an optional trailing #fragment) is
//     base64. Detected by the absence of an "@" before any "#".
//
// Shadowsocks does its own encryption, so no TLS block is ever rendered.
package shadowsocks

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

// Sentinel errors returned (wrapped in *ParseError) by Parse. Callers match
// them with errors.Is.
var (
	ErrInvalidLink     = errors.New("malformed link")
	ErrMissingPassword = errors.New("missing password")
	ErrMissingMethod   = errors.New("missing encryption method")
	ErrMissingHost     = errors.New("missing host")
	ErrMissingPort     = errors.New("missing port")
	ErrInvalidPort     = errors.New("invalid port")
)

// errSSBadBase64 is an internal sentinel used only to signal "none of the
// base64 variants decoded this" between the helpers below; it never escapes
// Parse (callers translate it to ErrInvalidLink).
var errSSBadBase64 = errors.New("shadowsocks: no base64 variant decoded userinfo")

// ParseError annotates a sentinel error with the offending field/value.
type ParseError struct {
	Field string
	Value string
	Err   error
}

func (e *ParseError) Error() string {
	if e.Value != "" {
		return fmt.Sprintf("shadowsocks: %s=%q: %v", e.Field, e.Value, e.Err)
	}
	return fmt.Sprintf("shadowsocks: %s: %v", e.Field, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

func parseErr(field, value string, err error) *ParseError {
	return &ParseError{Field: field, Value: value, Err: err}
}

// Params is everything a shadowsocks outbound needs, carved out of the link.
// Method is the cipher ("2022-blake3-aes-128-gcm", "aes-256-gcm",
// "chacha20-ietf-poly1305", …).
type Params struct {
	Host          string // bare host or IP; IPv6 stored WITHOUT brackets
	Port          uint16
	Password      string
	Method        string
	Plugin        string
	PluginOptions string
}

// Module implements protocol.Module, protocol.Relabeler and singbox.Renderer
// for Shadowsocks.
type Module struct{}

// New returns the Shadowsocks protocol module.
func New() Module { return Module{} }

// Descriptor implements protocol.Module.
func (Module) Descriptor() protocol.Descriptor {
	return protocol.Descriptor{
		Name:    "shadowsocks",
		Title:   "Shadowsocks",
		Kind:    protocol.KindOutbound,
		Input:   protocol.InputLink,
		Schemes: []string{"ss"},
	}
}

// Parse parses an ss:// share link. It only validates structure; it does not
// contact the network or verify keys.
func (Module) Parse(raw string) (protocol.Profile, error) {
	trimmed := strings.TrimSpace(raw)
	idx := strings.Index(trimmed, "://")
	if idx < 0 {
		return protocol.Profile{}, parseErr("link", raw, ErrInvalidLink)
	}
	if !strings.EqualFold(trimmed[:idx], "ss") {
		return protocol.Profile{}, parseErr("scheme", trimmed[:idx], ErrInvalidLink)
	}
	body := trimmed[idx+len("://"):]
	bodyNoFrag, _, _ := strings.Cut(body, "#")

	var method, password, host, portStr, name, pluginName, pluginOpts string

	if strings.Contains(bodyNoFrag, "@") {
		// SIP002.
		u, err := url.Parse(trimmed)
		if err != nil {
			return protocol.Profile{}, parseErr("link", raw, ErrInvalidLink)
		}
		if u.User == nil {
			return protocol.Profile{}, parseErr("password", "", ErrMissingPassword)
		}
		if pw, ok := u.User.Password(); ok {
			// An unescaped ':' was present in the raw userinfo: net/url
			// already split it into method/password for us.
			method, password = u.User.Username(), pw
		} else {
			userinfo := u.User.Username() // percent-decoded by net/url
			if decoded, derr := decodeSSBase64(userinfo); derr == nil {
				if m, p, ok := cutExactlyOneColon(string(decoded)); ok {
					method, password = m, p
				}
			}
			if method == "" && password == "" {
				// Base64 decoding failed, or didn't yield exactly one ':'.
				// Fall back to treating the percent-decoded userinfo itself
				// as "method:password" (this is how a %3A-escaped colon
				// shows up once net/url has unescaped it).
				if m, p, ok := cutExactlyOneColon(userinfo); ok {
					method, password = m, p
				}
			}
		}
		host = u.Hostname()
		portStr = u.Port()
		name = u.Fragment
		if plugin := u.Query().Get("plugin"); plugin != "" {
			pluginName, pluginOpts, _ = strings.Cut(plugin, ";")
		}
	} else {
		// Legacy: the whole body (minus fragment) is base64.
		_, fragment, hasFrag := strings.Cut(body, "#")
		if hasFrag {
			if dec, derr := url.PathUnescape(fragment); derr == nil {
				name = dec
			} else {
				name = fragment
			}
		}

		decoded, derr := decodeSSBase64(bodyNoFrag)
		if derr != nil {
			return protocol.Profile{}, parseErr("link", raw, ErrInvalidLink)
		}
		plain := string(decoded)
		at := strings.LastIndex(plain, "@")
		if at < 0 {
			return protocol.Profile{}, parseErr("link", raw, ErrInvalidLink)
		}
		methodPassword, hostPort := plain[:at], plain[at+1:]
		var ok bool
		method, password, ok = strings.Cut(methodPassword, ":")
		if !ok {
			return protocol.Profile{}, parseErr("link", raw, ErrInvalidLink)
		}
		h, p, serr := net.SplitHostPort(hostPort)
		if serr != nil {
			return protocol.Profile{}, parseErr("link", hostPort, ErrInvalidLink)
		}
		host, portStr = h, p
	}

	if method == "" {
		return protocol.Profile{}, parseErr("method", "", ErrMissingMethod)
	}
	if password == "" {
		return protocol.Profile{}, parseErr("password", "", ErrMissingPassword)
	}
	if host == "" {
		return protocol.Profile{}, parseErr("host", "", ErrMissingHost)
	}
	if portStr == "" {
		return protocol.Profile{}, parseErr("port", "", ErrMissingPort)
	}
	port64, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || port64 == 0 {
		return protocol.Profile{}, parseErr("port", portStr, ErrInvalidPort)
	}

	params := Params{
		Host:          host,
		Port:          uint16(port64),
		Password:      password,
		Method:        method,
		Plugin:        pluginName,
		PluginOptions: pluginOpts,
	}

	return protocol.Profile{
		Protocol: "shadowsocks",
		Label:    name,
		Raw:      raw,
		Params:   params,
	}, nil
}

// SetLabel implements protocol.Relabeler: it returns the link with its
// #fragment (display name) replaced, preserving everything else. This works
// uniformly for both SIP002 and legacy links, because both are valid URLs
// whose fragment carries the display name.
func (Module) SetLabel(raw, label string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", parseErr("link", trimmed, ErrInvalidLink)
	}
	u.Fragment = strings.TrimSpace(label)
	return u.String(), nil
}

// RenderNode implements singbox.Renderer, producing a ShadowsocksOutbound.
// Shadowsocks encrypts its own stream, so no `tls` block is ever emitted.
func (Module) RenderNode(p protocol.Profile, o singbox.RenderOpts) (any, error) {
	params, ok := p.Params.(Params)
	if !ok {
		return nil, fmt.Errorf("shadowsocks: unexpected Params type %T", p.Params)
	}
	return singbox.ShadowsocksOutbound{
		Type:           "shadowsocks",
		Tag:            o.Tag,
		Server:         params.Host,
		ServerPort:     int(params.Port),
		Method:         params.Method,
		Password:       params.Password,
		Plugin:         params.Plugin,
		PluginOpts:     params.PluginOptions,
		ConnectTimeout: o.ConnectTimeout,
		BindInterface:  o.BindInterface,
	}, nil
}

// decodeSSBase64 tries every base64 flavor share links use in the wild
// (standard/URL alphabet, padded/unpadded) and returns the first that
// decodes cleanly.
func decodeSSBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{
		base64.URLEncoding,
		base64.RawURLEncoding,
		base64.StdEncoding,
		base64.RawStdEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errSSBadBase64
}

// cutExactlyOneColon splits s into method/password on ':' only when s
// contains exactly one colon; otherwise it reports ok=false so callers can
// try the next fallback rather than guessing.
func cutExactlyOneColon(s string) (method, password string, ok bool) {
	if strings.Count(s, ":") != 1 {
		return "", "", false
	}
	method, password, _ = strings.Cut(s, ":")
	return method, password, true
}
