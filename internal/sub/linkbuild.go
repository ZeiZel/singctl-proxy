// Package sub — this file builds share links (vless://, vmess://, trojan://,
// ss://, hysteria2://, tuic://) from the neutral per-protocol fields the
// sing-box-JSON and Clash-YAML adapters (singbox_adapter.go, clash_adapter.go)
// carve out of a panel's own config shape. Both adapters share this file
// rather than each rendering query strings by hand, so the link-parameter
// names — which have to match internal/protocol/<name>'s Parse exactly, or
// the round trip silently produces a server nobody can connect to — are
// defined in exactly one place.
package sub

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// linkTLS is the TLS/REALITY layer shared by every protocol that can carry
// one, normalized from whichever panel format supplied it. RealityPub != ""
// is what marks REALITY as selected — the two source formats spell "reality
// enabled" differently (a nested enabled flag vs. a non-empty options block),
// so normalizing to "a key is present" lets every builder below share one
// check.
type linkTLS struct {
	Enabled        bool
	ServerName     string
	Insecure       bool
	ALPN           []string
	Fingerprint    string
	RealityPub     string
	RealityShortID string
}

func (t linkTLS) reality() bool { return t.RealityPub != "" }

// linkTransport is the stream-transport layer shared by vless/vmess/trojan.
// Mode and Extra are xhttp-only (Xray's own transport; upstream sing-box has
// no native concept of it, so panels that offer it tuck it onto the
// transport block one way or another — see the adapters' own doc comments).
type linkTransport struct {
	Type        string   // "", "tcp", "ws", "grpc", "http", "xhttp"
	ServiceName string   // grpc
	Path        string   // ws / http / xhttp
	Host        []string // ws / http / xhttp Host header
	Mode        string   // xhttp only
	Extra       string   // xhttp only, raw (compact-able) JSON
}

func hostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// setSecurityAndTLS writes the security/sni/fp/alpn/allowInsecure/pbk/sid
// params every TLS-capable protocol module (vless, trojan) parses the same
// way. It never writes security=none: that is every module's own default
// when the param is absent, so simply omitting it is equivalent and avoids
// trojan.Parse's hard rejection of an explicit "none".
func setSecurityAndTLS(q url.Values, tls linkTLS) {
	switch {
	case tls.reality():
		q.Set("security", "reality")
	case tls.Enabled:
		q.Set("security", "tls")
	}
	if tls.ServerName != "" {
		q.Set("sni", tls.ServerName)
	}
	if tls.Fingerprint != "" {
		q.Set("fp", tls.Fingerprint)
	}
	if len(tls.ALPN) > 0 {
		q.Set("alpn", strings.Join(tls.ALPN, ","))
	}
	if tls.Insecure {
		q.Set("allowInsecure", "1")
	}
	if tls.reality() {
		q.Set("pbk", tls.RealityPub)
		if tls.RealityShortID != "" {
			q.Set("sid", tls.RealityShortID)
		}
	}
}

// setTransport writes the type/serviceName/path/host(/mode/extra) params
// shared by vless and trojan. TCP (the default) is left unset, matching
// every module's own default.
func setTransport(q url.Values, tr linkTransport) {
	if tr.Type != "" && tr.Type != "tcp" {
		q.Set("type", tr.Type)
	}
	switch tr.Type {
	case "grpc":
		if tr.ServiceName != "" {
			q.Set("serviceName", tr.ServiceName)
		}
	case "ws", "http", "xhttp":
		if tr.Path != "" {
			q.Set("path", tr.Path)
		}
		if len(tr.Host) > 0 {
			q.Set("host", strings.Join(tr.Host, ","))
		}
	}
	if tr.Type == "xhttp" {
		if tr.Mode != "" {
			q.Set("mode", tr.Mode)
		}
		if tr.Extra != "" {
			q.Set("extra", tr.Extra)
		}
	}
}

// buildVLESSLink renders a vless:// link matching internal/protocol/vless's
// Parse exactly (params: type, security, pbk, sid, sni, fp, flow, alpn,
// host, path, serviceName, allowInsecure, mode, extra).
func buildVLESSLink(name, uuid, host string, port int, flow string, tls linkTLS, tr linkTransport) string {
	q := url.Values{}
	setSecurityAndTLS(q, tls)
	setTransport(q, tr)
	if flow != "" {
		q.Set("flow", flow)
	}
	u := url.URL{
		Scheme:   "vless",
		User:     url.User(uuid),
		Host:     hostPort(host, port),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String()
}

// buildTrojanLink renders a trojan:// link matching internal/protocol/trojan's
// Parse. Trojan is TLS-only, so "security=none" is never emitted — an absent
// security param is trojan.Parse's own TLS default.
func buildTrojanLink(name, password, host string, port int, tls linkTLS, tr linkTransport) string {
	q := url.Values{}
	setSecurityAndTLS(q, tls)
	setTransport(q, tr)
	u := url.URL{
		Scheme:   "trojan",
		User:     url.User(password),
		Host:     hostPort(host, port),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String()
}

// buildShadowsocksLink renders a SIP002 ss:// link matching
// internal/protocol/shadowsocks's Parse: the userinfo is base64(method:password).
func buildShadowsocksLink(name, method, password, host string, port int, plugin, pluginOpts string) string {
	userinfo := base64.URLEncoding.EncodeToString([]byte(method + ":" + password))
	q := url.Values{}
	if plugin != "" {
		if pluginOpts != "" {
			q.Set("plugin", plugin+";"+pluginOpts)
		} else {
			q.Set("plugin", plugin)
		}
	}
	u := url.URL{
		Scheme:   "ss",
		User:     url.User(userinfo),
		Host:     hostPort(host, port),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String()
}

// buildHysteria2Link renders a hysteria2:// link matching
// internal/protocol/hysteria2's Parse. Hysteria2 is TLS-only by protocol
// definition, so tls.Enabled is never consulted here — only the fields that
// actually vary (sni, alpn, insecure) are.
func buildHysteria2Link(name, password, host string, port, upMbps, downMbps int, obfsType, obfsPassword string, tls linkTLS) string {
	q := url.Values{}
	if tls.ServerName != "" {
		q.Set("sni", tls.ServerName)
	}
	if len(tls.ALPN) > 0 {
		q.Set("alpn", strings.Join(tls.ALPN, ","))
	}
	if tls.Insecure {
		q.Set("insecure", "1")
	}
	if obfsType != "" {
		q.Set("obfs", obfsType)
		if obfsPassword != "" {
			q.Set("obfs-password", obfsPassword)
		}
	}
	if upMbps > 0 {
		q.Set("up", strconv.Itoa(upMbps))
	}
	if downMbps > 0 {
		q.Set("down", strconv.Itoa(downMbps))
	}
	u := url.URL{
		Scheme:   "hysteria2",
		User:     url.User(password),
		Host:     hostPort(host, port),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String()
}

// buildTUICLink renders a tuic:// link matching internal/protocol/tuic's
// Parse. TUIC is TLS-only by protocol definition, same caveat as hysteria2
// above: tls.Enabled is never consulted.
func buildTUICLink(name, uuid, password, host string, port int, congestion, udpRelayMode string, zeroRTT bool, tls linkTLS) string {
	q := url.Values{}
	if tls.ServerName != "" {
		q.Set("sni", tls.ServerName)
	}
	if len(tls.ALPN) > 0 {
		q.Set("alpn", strings.Join(tls.ALPN, ","))
	}
	if tls.Insecure {
		q.Set("allow_insecure", "1")
	}
	if congestion != "" {
		q.Set("congestion_control", congestion)
	}
	if udpRelayMode != "" {
		q.Set("udp_relay_mode", udpRelayMode)
	}
	if zeroRTT {
		q.Set("zero_rtt_handshake", "1")
	}
	u := url.URL{
		Scheme:   "tuic",
		User:     url.UserPassword(uuid, password),
		Host:     hostPort(host, port),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String()
}

// vmessPayload mirrors the v2rayN/v2rayNG flat JSON object a vmess:// link's
// base64 payload carries (see internal/protocol/vmess's vmessJSON — same
// field names, since that is what Parse reads back).
type vmessPayload struct {
	V    string `json:"v"`
	PS   string `json:"ps"`
	Add  string `json:"add"`
	Port int    `json:"port"`
	ID   string `json:"id"`
	Aid  int    `json:"aid"`
	Scy  string `json:"scy"`
	Net  string `json:"net"`
	Host string `json:"host,omitempty"`
	Path string `json:"path,omitempty"`
	TLS  string `json:"tls,omitempty"`
	SNI  string `json:"sni,omitempty"`
	ALPN string `json:"alpn,omitempty"`
	FP   string `json:"fp,omitempty"`
}

// buildVMessLink renders a vmess:// link matching internal/protocol/vmess's
// Parse. Its "net" values are restricted to what Parse accepts (tcp, ws,
// grpc, h2 — "h2" being vmess's own name for what the neutral model calls
// TransportHTTP); a transport this package cannot express in that vocabulary
// (quic, httpupgrade, xhttp — vmess has no XHTTP convention) reports
// ok=false so the caller skips this server instead of emitting a link
// nothing can parse.
func buildVMessLink(name, uuid, host string, port, alterID int, tls linkTLS, tr linkTransport) (link string, ok bool) {
	var network string
	switch tr.Type {
	case "", "tcp":
		network = "tcp"
	case "ws":
		network = "ws"
	case "grpc":
		network = "grpc"
	case "http":
		network = "h2"
	default:
		return "", false
	}

	payload := vmessPayload{
		V: "2", PS: name, Add: host, Port: port, ID: uuid, Aid: alterID,
		Scy: "auto", Net: network,
	}
	if network == "grpc" {
		payload.Path = tr.ServiceName
	} else {
		payload.Path = tr.Path
	}
	if len(tr.Host) > 0 {
		payload.Host = tr.Host[0]
	}
	if tls.Enabled {
		payload.TLS = "tls"
		payload.SNI = tls.ServerName
		payload.FP = tls.Fingerprint
		if len(tls.ALPN) > 0 {
			payload.ALPN = strings.Join(tls.ALPN, ",")
		}
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(b), true
}
