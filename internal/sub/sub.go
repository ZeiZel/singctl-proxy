// Package sub models proxy subscriptions: the https:// URL a panel hands out
// (v2rayTun, Happ, Marzban, Remnawave, 3x-ui …) which returns a list of share
// links, refetched periodically to pick up server changes.
//
// Everything in this file is pure — body and header bytes in, links and
// metadata out — so the dialect handling is fully unit-testable. The network
// lives behind Fetcher (fetch.go).
package sub

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Meta is what a panel reports ALONGSIDE the links, in response headers. Every
// field is optional: plenty of panels send none of them.
type Meta struct {
	Title          string        `json:"title,omitempty"`           // profile-title
	UpdateInterval time.Duration `json:"update_interval,omitempty"` // profile-update-interval, in hours
	Upload         int64         `json:"upload,omitempty"`          // bytes used, upstream
	Download       int64         `json:"download,omitempty"`        // bytes used, downstream
	Total          int64         `json:"total,omitempty"`           // bytes in the plan; 0 = unknown/unlimited
	Expire         time.Time     `json:"expire,omitempty"`          // plan expiry; zero = none reported
}

// HasUsage reports whether the panel sent a usage/quota line worth showing.
func (m Meta) HasUsage() bool { return m.Total > 0 || m.Upload > 0 || m.Download > 0 }

// Used is the total traffic consumed.
func (m Meta) Used() int64 { return m.Upload + m.Download }

// Result is one successful fetch.
type Result struct {
	Links []string
	Meta  Meta
}

// knownSchemes are the share-link schemes a subscription body may contain. It
// mirrors the link-input modules' schemes (internal/protocol/<name>, wired up
// in internal/protocol/all) — kept as a plain list here rather than importing
// the registry so this package stays dependency-free and testable on its own.
var knownSchemes = []string{
	"vless", "vmess", "trojan", "ss", "hysteria2", "hy2", "hysteria", "hy", "tuic", "anytls",
}

// linkStart matches the beginning of any share link, used to split a body whose
// separator we cannot trust (see Parse).
var linkStart = regexp.MustCompile(`(?i)\b(` + strings.Join(knownSchemes, "|") + `)://`)

// Parse turns a subscription response body into share links.
//
// The format is a de-facto standard rather than a specified one, so this is
// deliberately tolerant of every dialect seen in the wild:
//
//   - the body is usually base64 of the link list, but some panels serve it as
//     plain text; base64 comes in all four flavours (std/URL, padded or not);
//   - links are usually newline-separated, but commas and semicolons occur too.
//     Splitting on commas naively would corrupt links, because a comma is legal
//     INSIDE one (alpn=h2,http/1.1), so the split is anchored on the next
//     link's scheme instead of on the separator;
//   - some panels serve something other than a link list entirely — a full
//     sing-box config, a Clash/Mihomo config, or a bare JSON array/object of
//     links — chosen by client User-Agent or an explicit query/path. Those
//     structured shapes are tried first (see parseStructured in format.go);
//     only once none of them claims the body does it fall through to the
//     base64/plain-text handling below, which is what every one of THIS
//     function's own tests still exercises unchanged.
//
// A body that decodes to nothing recognisable is an error rather than an empty
// list: silently loading zero servers looks identical to a working refresh and
// would quietly strand the user offline.
func Parse(body []byte) ([]string, error) {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return nil, fmt.Errorf("sub: empty response")
	}

	if links, err, handled := parseStructured(text); handled {
		if err != nil {
			return nil, err
		}
		return links, nil
	}

	if decoded, ok := decodeBase64(text); ok {
		text = decoded
	}

	links := splitLinks(text)
	if len(links) == 0 {
		return nil, fmt.Errorf("sub: no share links in the response (got %d bytes; is this a subscription URL?)", len(body))
	}
	return links, nil
}

// decodeBase64 tries every base64 flavour panels use. It reports ok only when
// the result actually looks like a link list, so a plain-text body that happens
// to be valid base64 is not mangled.
func decodeBase64(text string) (string, bool) {
	compact := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, text)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		decoded, err := enc.DecodeString(compact)
		if err != nil {
			continue
		}
		if linkStart.Match(decoded) {
			return string(decoded), true
		}
	}
	return "", false
}

// splitLinks cuts a blob into individual links by finding where each one
// starts, which sidesteps the separator problem entirely: whatever sits between
// two links (newline, comma, semicolon, stray whitespace) is discarded.
func splitLinks(text string) []string {
	idx := linkStart.FindAllStringIndex(text, -1)
	if len(idx) == 0 {
		return nil
	}
	out := make([]string, 0, len(idx))
	for i, loc := range idx {
		end := len(text)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		candidate := strings.TrimSpace(text[loc[0]:end])
		// Trim separators that trailed the link rather than preceding the next.
		candidate = strings.TrimRight(candidate, ",;\r\n\t ")
		if candidate != "" {
			out = append(out, candidate)
		}
	}
	return out
}

// ParseMeta reads the optional metadata headers a panel sends.
//
//	subscription-userinfo: upload=0; download=1234; total=107374182400; expire=1735689600
//	profile-update-interval: 12
//	profile-title: My Panel   (or base64: "base64:<...>")
func ParseMeta(h http.Header) Meta {
	var m Meta
	m.Title = decodeTitle(h.Get("profile-title"))
	if v := strings.TrimSpace(h.Get("profile-update-interval")); v != "" {
		if hours, err := strconv.Atoi(v); err == nil && hours > 0 {
			m.UpdateInterval = time.Duration(hours) * time.Hour
		}
	}
	for _, field := range strings.Split(h.Get("subscription-userinfo"), ";") {
		key, value, found := strings.Cut(strings.TrimSpace(field), "=")
		if !found {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "upload":
			m.Upload = n
		case "download":
			m.Download = n
		case "total":
			m.Total = n
		case "expire":
			if n > 0 {
				m.Expire = time.Unix(n, 0)
			}
		}
	}
	return m
}

// decodeTitle handles the "base64:<payload>" form some panels use for titles
// carrying non-ASCII, which a raw HTTP header cannot hold.
func decodeTitle(raw string) string {
	raw = strings.TrimSpace(raw)
	payload, found := strings.CutPrefix(raw, "base64:")
	if !found {
		return raw
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if decoded, err := enc.DecodeString(strings.TrimSpace(payload)); err == nil {
			return string(decoded)
		}
	}
	return raw
}
