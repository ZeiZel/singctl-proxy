package ui

import (
	"net/url"
	"strings"
)

// maskLink renders a VLESS link like a password: the secret body is replaced
// with bullets, while the human label (the #fragment) is kept visible so the
// user can tell which server it is without exposing the UUID/host. maskChar is
// "•" on Unicode terminals, "*" on ASCII.
func maskLink(link, maskChar string) string {
	const bullets = 12
	masked := strings.Repeat(maskChar, bullets)
	if i := strings.LastIndex(link, "#"); i >= 0 && i+1 < len(link) {
		if name, err := url.QueryUnescape(link[i+1:]); err == nil && strings.TrimSpace(name) != "" {
			return masked + "  " + name
		}
	}
	return masked
}

// linkName returns the human label (decoded #fragment) of a VLESS link, or "".
func linkName(link string) string {
	if i := strings.LastIndex(link, "#"); i >= 0 && i+1 < len(link) {
		if name, err := url.QueryUnescape(link[i+1:]); err == nil {
			return strings.TrimSpace(name)
		}
	}
	return ""
}
