package xhttp

import (
	"crypto/rand"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/http2/hpack"
)

// The XHTTP server REJECTS a request whose padding length falls outside
// xPaddingBytes (HTTP 400), so padding is mandatory, not cosmetic. 'X' and 'Z'
// are the two base62 characters HPACK/QPACK encode in a full 8 bits, so a run
// of them keeps its length after h2/h3 header compression — which is the point
// of the padding in the first place.
const (
	charsetBase62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	// avgHuffmanBytesPerCharBase62 is Xray's measured ~20% Huffman shrink for
	// random base62 text; it seeds the tokenish generator's length guess.
	avgHuffmanBytesPerCharBase62 = 0.8
	// tokenishTolerance is how far (in post-Huffman bytes) the tokenish padding
	// may land from the target before the server would reject it.
	tokenishTolerance = 2
)

// randBetween draws uniformly from [from,to]. A degenerate or inverted range
// collapses to from, matching Xray's crypto.RandBetween.
func randBetween(from, to int32) int32 {
	if to <= from {
		return from
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(to-from)+1))
	if err != nil {
		return from
	}
	return from + int32(n.Int64())
}

func (r Range) rand() int32 { return randBetween(r.From, r.To) }

// randBase62 returns n cryptographically random base62 characters.
func randBase62(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strings.Repeat("X", n)
	}
	for i, v := range b {
		b[i] = charsetBase62[int(v)%len(charsetBase62)]
	}
	return string(b)
}

// tokenishPadding produces random base62 text whose HUFFMAN-ENCODED length is
// within tokenishTolerance of target — the length the server measures when the
// tokenish method is in use.
func tokenishPadding(target int) string {
	n := int(math.Ceil(float64(target) / avgHuffmanBytesPerCharBase62))
	if n < 1 {
		n = 1
	}
	s := randBase62(n)
	adjust := byte('X')
	for range 150 {
		diff := int(hpack.HuffmanEncodeLength(s)) - target
		if diff <= tokenishTolerance && diff >= -tokenishTolerance {
			return s
		}
		if diff < 0 {
			// Too short: extend, alternating X/Z so it does not become an
			// obvious run of one character.
			s += string(adjust)
			if adjust == 'X' {
				adjust = 'Z'
			} else {
				adjust = 'X'
			}
			continue
		}
		if len(s) <= 1 {
			return s
		}
		s = s[:len(s)-1]
	}
	return s
}

// generatePadding builds a padding value of the given length under method
// ("repeat-x", "tokenish", or "" which behaves as repeat-x).
func generatePadding(method string, length int) string {
	if length <= 0 {
		return ""
	}
	switch method {
	case PaddingTokenish:
		if s := tokenishPadding(length); s != "" {
			return s
		}
		return strings.Repeat("X", length)
	default: // PaddingRepeatX and the unset default
		return strings.Repeat("X", length)
	}
}

// applyPadding attaches the mandatory padding to req. It must run BEFORE the
// session id / sequence number are appended to the path: the "queryInHeader"
// placement (the default, a Referer header) embeds a copy of the request URL as
// it stands at this point, and the server reconstructs it the same way.
func (s Settings) applyPadding(req *http.Request) {
	placement, key, header, method := s.paddingParams()
	value := generatePadding(method, int(s.paddingBytes().rand()))
	if value == "" {
		return
	}
	switch placement {
	case PlacementHeader:
		req.Header.Set(header, value)
	case PlacementQueryInHeader:
		u := *req.URL
		u.RawQuery = key + "=" + value
		req.Header.Set(header, u.String())
	case PlacementCookie:
		req.AddCookie(&http.Cookie{Name: key, Value: value, Path: "/"})
	case PlacementQuery:
		applyToQuery(req.URL, key, value)
	}
}

func applyToQuery(u *url.URL, key, value string) {
	if u == nil || key == "" || value == "" {
		return
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
}

// applyMeta places the session id and sequence number where the server expects
// to read them back (path segments by default).
func (s Settings) applyMeta(req *http.Request, sessionID, seq string) {
	if sessionID != "" {
		switch s.sessionPlacement() {
		case PlacementPath:
			req.URL.Path = appendPathSegment(req.URL.Path, sessionID)
		case PlacementQuery:
			applyToQuery(req.URL, s.sessionKey(), sessionID)
		case PlacementHeader:
			req.Header.Set(s.sessionKey(), sessionID)
		case PlacementCookie:
			req.AddCookie(&http.Cookie{Name: s.sessionKey(), Value: sessionID})
		}
	}
	if seq != "" {
		switch s.seqPlacement() {
		case PlacementPath:
			req.URL.Path = appendPathSegment(req.URL.Path, seq)
		case PlacementQuery:
			applyToQuery(req.URL, s.seqKey(), seq)
		case PlacementHeader:
			req.Header.Set(s.seqKey(), seq)
		case PlacementCookie:
			req.AddCookie(&http.Cookie{Name: s.seqKey(), Value: seq})
		}
	}
}

func appendPathSegment(path, value string) string {
	if strings.HasSuffix(path, "/") {
		return path + value
	}
	return path + "/" + value
}
