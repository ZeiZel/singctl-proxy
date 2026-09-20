// Package sub — this file adapts Clash/Mihomo's proxies: YAML shape into
// share links, for panels (Remnawave, Marzban, Marzneshin, Hiddify, 3x-ui,
// …) that serve a Clash-format subscription instead of a link list.
//
// Only the top-level `proxies:` list matters; proxy-groups and rules (which
// exist in a real Clash config) describe client-side routing, which singctl
// builds itself, so they are never even unmarshaled here.
//
// clashProxy's field names are Mihomo's own YAML keys — kebab-case, unlike
// sing-box's snake_case, and read off the well-known Mihomo proxy schema
// rather than guessed.
package sub

import (
	"encoding/json"
	"fmt"
)

type clashDoc struct {
	Proxies []clashProxy `yaml:"proxies"`
}

type clashWSOpts struct {
	Path    string            `yaml:"path"`
	Headers map[string]string `yaml:"headers"`
}

type clashGRPCOpts struct {
	ServiceName string `yaml:"grpc-service-name"`
}

type clashRealityOpts struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id"`
}

// clashXHTTPOpts mirrors Mihomo's vless xhttp-opts block. Extra is decoded
// as a generic map (rather than a raw string, which YAML has no notion of)
// and re-marshaled to JSON when building the link, since that is the form
// internal/protocol/vless's `extra=` param expects.
type clashXHTTPOpts struct {
	Path  string         `yaml:"path"`
	Host  string         `yaml:"host"`
	Mode  string         `yaml:"mode"`
	Extra map[string]any `yaml:"extra"`
}

type clashProxy struct {
	Type              string            `yaml:"type"`
	Name              string            `yaml:"name"`
	Server            string            `yaml:"server"`
	Port              int               `yaml:"port"`
	UUID              string            `yaml:"uuid"`
	Password          string            `yaml:"password"`
	Cipher            string            `yaml:"cipher"`
	AlterID           int               `yaml:"alterId"`
	Network           string            `yaml:"network"`
	TLS               bool              `yaml:"tls"`
	ServerName        string            `yaml:"servername"`
	SkipCertVerify    bool              `yaml:"skip-cert-verify"`
	ALPN              []string          `yaml:"alpn"`
	ClientFingerprint string            `yaml:"client-fingerprint"`
	Flow              string            `yaml:"flow"`
	WSOpts            *clashWSOpts      `yaml:"ws-opts"`
	GRPCOpts          *clashGRPCOpts    `yaml:"grpc-opts"`
	XHTTPOpts         *clashXHTTPOpts   `yaml:"xhttp-opts"`
	RealityOpts       *clashRealityOpts `yaml:"reality-opts"`
	Plugin            string            `yaml:"plugin"`
	Obfs              string            `yaml:"obfs"`
	ObfsPassword      string            `yaml:"obfs-password"`
	Up                int               `yaml:"up"`
	Down              int               `yaml:"down"`
	CongestionControl string            `yaml:"congestion-controller"`
	UDPRelayMode      string            `yaml:"udp-relay-mode"`
	ReduceRTT         bool              `yaml:"reduce-rtt"`
}

// tls normalizes this proxy's TLS/REALITY fields. reality-opts implies TLS
// even when `tls:` was left unset in the source YAML — REALITY still
// terminates a TLS handshake at the edge, and some generators omit the
// redundant flag.
func (p clashProxy) tls() linkTLS {
	lt := linkTLS{Enabled: p.TLS, ServerName: p.ServerName, Insecure: p.SkipCertVerify, ALPN: p.ALPN, Fingerprint: p.ClientFingerprint}
	if p.RealityOpts != nil && p.RealityOpts.PublicKey != "" {
		lt.Enabled = true
		lt.RealityPub = p.RealityOpts.PublicKey
		lt.RealityShortID = p.RealityOpts.ShortID
	}
	return lt
}

func (p clashProxy) transport() linkTransport {
	lt := linkTransport{Type: p.Network}
	switch p.Network {
	case "ws":
		if p.WSOpts != nil {
			lt.Path = p.WSOpts.Path
			if host := p.WSOpts.Headers["Host"]; host != "" {
				lt.Host = []string{host}
			}
		}
	case "grpc":
		if p.GRPCOpts != nil {
			lt.ServiceName = p.GRPCOpts.ServiceName
		}
	case "xhttp":
		if p.XHTTPOpts != nil {
			lt.Path = p.XHTTPOpts.Path
			if p.XHTTPOpts.Host != "" {
				lt.Host = []string{p.XHTTPOpts.Host}
			}
			lt.Mode = p.XHTTPOpts.Mode
			if len(p.XHTTPOpts.Extra) > 0 {
				if b, err := json.Marshal(p.XHTTPOpts.Extra); err == nil {
					lt.Extra = string(b)
				}
			}
		}
	}
	return lt
}

// clashLinks converts a Clash/Mihomo proxies: list into share links,
// skipping any proxy type we do not (or cannot) render as a link — relay,
// direct, or any protocol this package does not support — rather than
// failing the whole subscription.
func clashLinks(proxies []clashProxy) ([]string, error) {
	var links []string
	for _, p := range proxies {
		switch p.Type {
		case "vless":
			if p.Server != "" && p.UUID != "" {
				links = append(links, buildVLESSLink(p.Name, p.UUID, p.Server, p.Port, p.Flow, p.tls(), p.transport()))
			}
		case "vmess":
			if p.Server != "" && p.UUID != "" {
				if link, ok := buildVMessLink(p.Name, p.UUID, p.Server, p.Port, p.AlterID, p.tls(), p.transport()); ok {
					links = append(links, link)
				}
			}
		case "trojan":
			if p.Server != "" && p.Password != "" {
				links = append(links, buildTrojanLink(p.Name, p.Password, p.Server, p.Port, p.tls(), p.transport()))
			}
		case "ss":
			if p.Server != "" && p.Cipher != "" {
				links = append(links, buildShadowsocksLink(p.Name, p.Cipher, p.Password, p.Server, p.Port, p.Plugin, ""))
			}
		case "hysteria2":
			if p.Server != "" {
				links = append(links, buildHysteria2Link(p.Name, p.Password, p.Server, p.Port, p.Up, p.Down, p.Obfs, p.ObfsPassword, p.tls()))
			}
		case "tuic":
			if p.Server != "" && p.UUID != "" {
				links = append(links, buildTUICLink(p.Name, p.UUID, p.Password, p.Server, p.Port, p.CongestionControl, p.UDPRelayMode, p.ReduceRTT, p.tls()))
			}
		default:
			// direct, reject, relay, dns, and any protocol we don't support.
		}
	}
	if len(links) == 0 {
		return nil, fmt.Errorf("sub: Clash config has no proxy type we support")
	}
	return links, nil
}
