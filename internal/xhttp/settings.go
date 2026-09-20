// Package xhttp implements the client side of Xray's XHTTP transport (formerly
// SplitHTTP): a stream tunnelled over ordinary HTTP requests, so it survives
// CDNs and HTTP-aware middleboxes that kill WebSocket/gRPC.
//
// sing-box has no XHTTP transport (its V2Ray transport union is http / ws /
// quic / grpc / httpupgrade), so keys with `type=xhttp` cannot be expressed as
// a stock sing-box outbound. This package provides the transport as a plain
// net.Conn factory with NO sing-box dependency — it takes a dial function that
// hands it an already-connected (and, where applicable, already-TLS-handshaked)
// net.Conn, and everything above it (VLESS, TLS/REALITY, the sing-box outbound
// wiring) lives in internal/singboxext.
//
// The wire format mirrors Xray-core v26's transport/internet/splithttp: the
// downlink is one long-lived response body, the uplink is either one long-lived
// request body (stream-up), a sequence of numbered POSTs (packet-up), or the
// request body of the same request that carries the downlink (stream-one).
package xhttp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Upload/session/padding placements, mirroring Xray's placement constants.
const (
	PlacementQueryInHeader = "queryInHeader"
	PlacementCookie        = "cookie"
	PlacementHeader        = "header"
	PlacementQuery         = "query"
	PlacementPath          = "path"
	PlacementBody          = "body"
	PlacementAuto          = "auto"
)

// Transport modes. ModeAuto is resolved by Config.resolveMode.
const (
	ModeAuto      = "auto"
	ModePacketUp  = "packet-up"
	ModeStreamUp  = "stream-up"
	ModeStreamOne = "stream-one"
)

// Padding generation methods.
const (
	PaddingRepeatX  = "repeat-x"
	PaddingTokenish = "tokenish"
)

// Range is Xray's Int32Range: a closed [From,To] interval a value is drawn from
// uniformly at random. In JSON it is either a number (5), a single-value string
// ("5"), or a range string ("100-1000").
type Range struct {
	From int32
	To   int32
}

func (r *Range) UnmarshalJSON(data []byte) error {
	var n int32
	if err := json.Unmarshal(data, &n); err == nil {
		r.From, r.To = n, n
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("xhttp: range must be a number or string, got %s", string(data))
	}
	s = strings.TrimSpace(s)
	from, to, found := strings.Cut(s, "-")
	if !found {
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return fmt.Errorf("xhttp: invalid range %q", s)
		}
		r.From, r.To = int32(n), int32(n)
		return nil
	}
	fromN, err := strconv.ParseInt(strings.TrimSpace(from), 10, 32)
	if err != nil {
		return fmt.Errorf("xhttp: invalid range %q", s)
	}
	toN, err := strconv.ParseInt(strings.TrimSpace(to), 10, 32)
	if err != nil {
		return fmt.Errorf("xhttp: invalid range %q", s)
	}
	r.From, r.To = int32(fromN), int32(toN)
	return nil
}

// empty reports whether the range was left unset (so a default applies).
func (r Range) empty() bool { return r.To == 0 }

// or returns r, or the fallback range when r is unset.
func (r Range) or(from, to int32) Range {
	if r.empty() {
		return Range{From: from, To: to}
	}
	return r
}

// Settings mirrors the client-relevant subset of Xray's `xhttpSettings` object,
// which panels ship inside a share link's `extra=` parameter. Field names and
// defaults follow Xray-core v26 exactly — the server validates several of them
// (notably the padding length), so drifting from its defaults breaks the
// connection with an opaque HTTP 400.
//
// Server-only and unsupported knobs (scMaxBufferedPosts, scStreamUpServerSecs,
// serverMaxHeaderBytes, xmux, downloadSettings) are deliberately absent: they
// either do not affect what the client puts on the wire or are documented as
// unsupported in Config.Validate.
type Settings struct {
	Host    string            `json:"host"`
	Path    string            `json:"path"`
	Mode    string            `json:"mode"`
	Headers map[string]string `json:"headers"`

	XPaddingBytes     Range  `json:"xPaddingBytes"`
	XPaddingObfsMode  bool   `json:"xPaddingObfsMode"`
	XPaddingKey       string `json:"xPaddingKey"`
	XPaddingHeader    string `json:"xPaddingHeader"`
	XPaddingPlacement string `json:"xPaddingPlacement"`
	XPaddingMethod    string `json:"xPaddingMethod"`

	UplinkHTTPMethod string `json:"uplinkHTTPMethod"`

	SessionPlacement string `json:"sessionPlacement"`
	SessionKey       string `json:"sessionKey"`
	SeqPlacement     string `json:"seqPlacement"`
	SeqKey           string `json:"seqKey"`

	UplinkDataPlacement string `json:"uplinkDataPlacement"`
	UplinkDataKey       string `json:"uplinkDataKey"`
	UplinkChunkSize     Range  `json:"uplinkChunkSize"`

	NoGRPCHeader bool `json:"noGRPCHeader"`

	ScMaxEachPostBytes   Range `json:"scMaxEachPostBytes"`
	ScMinPostsIntervalMs Range `json:"scMinPostsIntervalMs"`
}

// ParseSettings decodes an `extra=` blob from a share link. An empty blob
// yields zero-valued settings (all defaults).
func ParseSettings(extra string) (Settings, error) {
	var s Settings
	extra = strings.TrimSpace(extra)
	if extra == "" {
		return s, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(extra)))
	if err := dec.Decode(&s); err != nil {
		return Settings{}, fmt.Errorf("xhttp: invalid extra settings: %w", err)
	}
	return s, nil
}

// --- normalized accessors (mirroring Xray's GetNormalized* methods) ---

func (s Settings) paddingBytes() Range { return s.XPaddingBytes.or(100, 1000) }

func (s Settings) maxEachPostBytes() Range { return s.ScMaxEachPostBytes.or(1000000, 1000000) }

func (s Settings) minPostsIntervalMs() Range { return s.ScMinPostsIntervalMs.or(30, 30) }

func (s Settings) uplinkMethod() string {
	if s.UplinkHTTPMethod == "" {
		return "POST"
	}
	return s.UplinkHTTPMethod
}

func (s Settings) sessionPlacement() string {
	if s.SessionPlacement == "" {
		return PlacementPath
	}
	return s.SessionPlacement
}

func (s Settings) seqPlacement() string {
	if s.SeqPlacement == "" {
		return PlacementPath
	}
	return s.SeqPlacement
}

// uplinkDataPlacement resolves to "body" when unset. Xray's "auto" means the
// same thing on the client side (its FillPacketRequest treats auto and body
// identically); only the server splices the three sources back together.
func (s Settings) uplinkDataPlacement() string {
	if s.UplinkDataPlacement == "" {
		return PlacementBody
	}
	return s.UplinkDataPlacement
}

func (s Settings) sessionKey() string {
	if s.SessionKey != "" {
		return s.SessionKey
	}
	switch s.sessionPlacement() {
	case PlacementHeader:
		return "X-Session"
	case PlacementCookie, PlacementQuery:
		return "x_session"
	default:
		return ""
	}
}

func (s Settings) seqKey() string {
	if s.SeqKey != "" {
		return s.SeqKey
	}
	switch s.seqPlacement() {
	case PlacementHeader:
		return "X-Seq"
	case PlacementCookie, PlacementQuery:
		return "x_seq"
	default:
		return ""
	}
}

// uplinkChunkSize is the size of each header/cookie chunk the base64 payload is
// split into; it is irrelevant for the (default) body placement.
func (s Settings) uplinkChunkSize() Range {
	if s.UplinkChunkSize.empty() {
		switch s.UplinkDataPlacement {
		case PlacementCookie:
			return Range{From: 2 * 1024, To: 3 * 1024}
		case PlacementHeader:
			return Range{From: 3 * 1000, To: 4 * 1000}
		default:
			return s.maxEachPostBytes()
		}
	}
	if s.UplinkChunkSize.From < 64 {
		return Range{From: 64, To: max(64, s.UplinkChunkSize.To)}
	}
	return s.UplinkChunkSize
}

// paddingParams returns the placement/key/header/method actually used for the
// X-Padding value. Outside obfuscation mode Xray hardcodes the padding into a
// Referer URL query named x_padding, ignoring the configured placement.
func (s Settings) paddingParams() (placement, key, header, method string) {
	if !s.XPaddingObfsMode {
		return PlacementQueryInHeader, "x_padding", "Referer", ""
	}
	placement, key, header, method = s.XPaddingPlacement, s.XPaddingKey, s.XPaddingHeader, s.XPaddingMethod
	if placement == "" {
		placement = PlacementQueryInHeader
	}
	if key == "" {
		key = "x_padding"
	}
	if header == "" {
		header = "X-Padding"
	}
	if method == "" {
		method = PaddingRepeatX
	}
	return placement, key, header, method
}
