// Package sub — this file adapts sing-box's own JSON config shape (see
// internal/singbox/schema.go, the authoritative outbound schema) into share
// links, for panels (Remnawave, Marzban, Marzneshin, Hiddify, 3x-ui, …) that
// serve a full sing-box config instead of a link list.
//
// Only the outbounds array matters: everything else in the config (log, dns,
// inbounds, route) describes how a sing-box client should route, which
// singctl builds itself regardless of what the panel suggests. Non-server
// outbounds (direct, block, dns-adjacent selectors, urltest/selector groups)
// are skipped the same way an unsupported protocol is — a mixed outbounds
// array must still yield every server we CAN use.
//
// The structs below mirror schema.go's JSON field names (the authoritative
// shape sing-box itself expects — server_port, server_name, public_key, …)
// rather than importing schema.go's types directly: some panels tuck
// Xray-only XHTTP knobs (mode/extra) onto the transport block even though
// upstream sing-box's own Transport type has no such fields, and decoding
// tolerantly here — extra fields simply parsed if present, ignored if not —
// is simpler than teaching schema.go about a client-side convention it was
// never meant to carry.
package sub

import (
	"encoding/json"
	"fmt"
)

type sbTLS struct {
	Enabled    bool       `json:"enabled"`
	ServerName string     `json:"server_name"`
	Insecure   bool       `json:"insecure"`
	ALPN       []string   `json:"alpn"`
	UTLS       *sbUTLS    `json:"utls"`
	Reality    *sbReality `json:"reality"`
}

type sbUTLS struct {
	Fingerprint string `json:"fingerprint"`
}

type sbReality struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key"`
	ShortID   string `json:"short_id"`
}

// toLinkTLS normalizes a (possibly absent) TLS block. A nil receiver is the
// common case — direct/block entries, and any protocol parsed before its TLS
// pointer is known to exist — so this is safe to call unconditionally.
func (t *sbTLS) toLinkTLS() linkTLS {
	if t == nil {
		return linkTLS{}
	}
	lt := linkTLS{Enabled: t.Enabled, ServerName: t.ServerName, Insecure: t.Insecure, ALPN: t.ALPN}
	if t.UTLS != nil {
		lt.Fingerprint = t.UTLS.Fingerprint
	}
	if t.Reality != nil && t.Reality.Enabled {
		lt.RealityPub = t.Reality.PublicKey
		lt.RealityShortID = t.Reality.ShortID
	}
	return lt
}

// sbTransport mirrors sing-box's transport block, plus Mode/Extra for the
// Xray-only xhttp convention some panels tuck onto it (see package doc).
type sbTransport struct {
	Type        string          `json:"type"`
	ServiceName string          `json:"service_name"`
	Path        string          `json:"path"`
	Host        []string        `json:"host"`
	Mode        string          `json:"mode"`
	Extra       json.RawMessage `json:"extra"`
}

func (t *sbTransport) toLinkTransport() linkTransport {
	if t == nil {
		return linkTransport{}
	}
	lt := linkTransport{Type: t.Type, ServiceName: t.ServiceName, Path: t.Path, Host: t.Host, Mode: t.Mode}
	if len(t.Extra) > 0 {
		lt.Extra = string(t.Extra)
	}
	return lt
}

type sbOutboundHead struct {
	Type string `json:"type"`
}

type sbVLESS struct {
	Tag        string       `json:"tag"`
	Server     string       `json:"server"`
	ServerPort int          `json:"server_port"`
	UUID       string       `json:"uuid"`
	Flow       string       `json:"flow"`
	TLS        *sbTLS       `json:"tls"`
	Transport  *sbTransport `json:"transport"`
}

type sbVMess struct {
	Tag        string       `json:"tag"`
	Server     string       `json:"server"`
	ServerPort int          `json:"server_port"`
	UUID       string       `json:"uuid"`
	AlterID    int          `json:"alter_id"`
	TLS        *sbTLS       `json:"tls"`
	Transport  *sbTransport `json:"transport"`
}

type sbTrojan struct {
	Tag        string       `json:"tag"`
	Server     string       `json:"server"`
	ServerPort int          `json:"server_port"`
	Password   string       `json:"password"`
	TLS        *sbTLS       `json:"tls"`
	Transport  *sbTransport `json:"transport"`
}

type sbShadowsocks struct {
	Tag        string `json:"tag"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Method     string `json:"method"`
	Password   string `json:"password"`
	Plugin     string `json:"plugin"`
	PluginOpts string `json:"plugin_opts"`
}

type sbHysteria2Obfs struct {
	Type     string `json:"type"`
	Password string `json:"password"`
}

type sbHysteria2 struct {
	Tag        string           `json:"tag"`
	Server     string           `json:"server"`
	ServerPort int              `json:"server_port"`
	UpMbps     int              `json:"up_mbps"`
	DownMbps   int              `json:"down_mbps"`
	Obfs       *sbHysteria2Obfs `json:"obfs"`
	Password   string           `json:"password"`
	TLS        *sbTLS           `json:"tls"`
}

type sbTUIC struct {
	Tag               string `json:"tag"`
	Server            string `json:"server"`
	ServerPort        int    `json:"server_port"`
	UUID              string `json:"uuid"`
	Password          string `json:"password"`
	CongestionControl string `json:"congestion_control"`
	UDPRelayMode      string `json:"udp_relay_mode"`
	ZeroRTTHandshake  bool   `json:"zero_rtt_handshake"`
	TLS               *sbTLS `json:"tls"`
}

// singboxOutbounds converts a sing-box config's `outbounds` array into share
// links, skipping every non-server entry (direct, block, dns, selector,
// urltest, and any protocol this package cannot render as a link, e.g.
// wireguard or shadowtls) rather than failing the whole subscription.
func singboxOutbounds(raw json.RawMessage) ([]string, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("sub: invalid sing-box outbounds array: %w", err)
	}

	var links []string
	for _, entry := range entries {
		var head sbOutboundHead
		if err := json.Unmarshal(entry, &head); err != nil {
			continue
		}
		switch head.Type {
		case "vless":
			var o sbVLESS
			if json.Unmarshal(entry, &o) == nil && o.Server != "" && o.UUID != "" {
				links = append(links, buildVLESSLink(o.Tag, o.UUID, o.Server, o.ServerPort, o.Flow, o.TLS.toLinkTLS(), o.Transport.toLinkTransport()))
			}
		case "vmess":
			var o sbVMess
			if json.Unmarshal(entry, &o) == nil && o.Server != "" && o.UUID != "" {
				if link, ok := buildVMessLink(o.Tag, o.UUID, o.Server, o.ServerPort, o.AlterID, o.TLS.toLinkTLS(), o.Transport.toLinkTransport()); ok {
					links = append(links, link)
				}
			}
		case "trojan":
			var o sbTrojan
			if json.Unmarshal(entry, &o) == nil && o.Server != "" && o.Password != "" {
				links = append(links, buildTrojanLink(o.Tag, o.Password, o.Server, o.ServerPort, o.TLS.toLinkTLS(), o.Transport.toLinkTransport()))
			}
		case "shadowsocks":
			var o sbShadowsocks
			if json.Unmarshal(entry, &o) == nil && o.Server != "" && o.Method != "" {
				links = append(links, buildShadowsocksLink(o.Tag, o.Method, o.Password, o.Server, o.ServerPort, o.Plugin, o.PluginOpts))
			}
		case "hysteria2":
			var o sbHysteria2
			if json.Unmarshal(entry, &o) == nil && o.Server != "" {
				var obfsType, obfsPassword string
				if o.Obfs != nil {
					obfsType, obfsPassword = o.Obfs.Type, o.Obfs.Password
				}
				links = append(links, buildHysteria2Link(o.Tag, o.Password, o.Server, o.ServerPort, o.UpMbps, o.DownMbps, obfsType, obfsPassword, o.TLS.toLinkTLS()))
			}
		case "tuic":
			var o sbTUIC
			if json.Unmarshal(entry, &o) == nil && o.Server != "" && o.UUID != "" {
				links = append(links, buildTUICLink(o.Tag, o.UUID, o.Password, o.Server, o.ServerPort, o.CongestionControl, o.UDPRelayMode, o.ZeroRTTHandshake, o.TLS.toLinkTLS()))
			}
		default:
			// direct, block, dns, selector, urltest, wireguard, shadowtls,
			// http, socks, and anything else: not a server we can render as
			// a share link.
		}
	}
	if len(links) == 0 {
		return nil, fmt.Errorf("sub: sing-box config has no outbound protocol we support")
	}
	return links, nil
}
