package xhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

// HTTP versions the transport can speak. Xray derives this from the TLS ALPN;
// the caller (internal/singboxext) does the same and passes the result in.
const (
	HTTPVersion11 = "1.1"
	HTTPVersion2  = "2"
	HTTPVersion3  = "3"
)

const (
	// idleConnTimeout mirrors Xray's net.ConnIdleTimeout.
	idleConnTimeout = 300 * time.Second
	// h2KeepAlive mirrors Chrome's H2 ping interval, which Xray copies so the
	// idle downlink connection is not reaped by middleboxes.
	h2KeepAlive = 45 * time.Second
)

// Config is everything the dialer needs to talk to one XHTTP server.
type Config struct {
	// Dial returns a fresh connection to the server, already TLS-handshaked
	// when the profile uses TLS or REALITY. The transport calls it once per
	// underlying HTTP connection, not once per stream.
	Dial func(ctx context.Context) (net.Conn, error)

	// Scheme is "https" when Dial performs a TLS handshake, "http" otherwise.
	Scheme string
	// Authority is the URL host: the link's `host=` parameter, else the SNI,
	// else the server address. It becomes the Host header / :authority.
	Authority string
	// HTTPVersion is "1.1" or "2" (see decide rules in internal/singboxext).
	HTTPVersion string
	// Reality reports whether the connection runs over REALITY, which changes
	// how mode "auto" resolves — exactly as in Xray.
	Reality bool
	// UserAgent overrides the default browser-ish User-Agent header.
	UserAgent string

	// Settings are the xhttpSettings from the link (path/host/mode plus the
	// `extra=` blob). Path and Mode here win over Settings.Path/Mode when set.
	Settings Settings
}

const defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// Dialer opens XHTTP streams to one server. It is safe for concurrent use and
// must be closed when the owning outbound goes away.
type Dialer struct {
	cfg      Config
	settings Settings
	mode     string
	baseURL  url.URL

	// download carries the long-lived streaming response (and, for h2, every
	// request — h2 multiplexes them on one connection).
	download *http.Client
	// upload is a separate HTTP/1.1 client for packet-up POSTs. The download
	// client must disable keep-alives (a chunked response body pins the
	// connection for the life of the stream), which would otherwise force a
	// fresh TCP+TLS handshake for every uploaded chunk. Nil for HTTP/2.
	upload *http.Client
}

// New validates cfg and builds the dialer.
func New(cfg Config) (*Dialer, error) {
	if cfg.Dial == nil {
		return nil, errors.New("xhttp: Dial is required")
	}
	switch cfg.HTTPVersion {
	case HTTPVersion11, HTTPVersion2:
	case HTTPVersion3:
		return nil, errors.New("xhttp: HTTP/3 (alpn=h3) is not supported; drop h3 from the key's alpn")
	default:
		return nil, fmt.Errorf("xhttp: unsupported http version %q", cfg.HTTPVersion)
	}
	if cfg.Authority == "" {
		return nil, errors.New("xhttp: Authority is required")
	}
	if cfg.Scheme != "http" && cfg.Scheme != "https" {
		return nil, fmt.Errorf("xhttp: unsupported scheme %q", cfg.Scheme)
	}

	s := cfg.Settings
	path, query := splitPathQuery(s.Path)

	d := &Dialer{
		cfg:      cfg,
		settings: s,
		mode:     resolveMode(s.Mode, cfg.Reality),
		baseURL: url.URL{
			Scheme:   cfg.Scheme,
			Host:     cfg.Authority,
			Path:     path,
			RawQuery: query,
		},
	}

	dial := func(ctx context.Context) (net.Conn, error) { return cfg.Dial(ctx) }
	if cfg.HTTPVersion == HTTPVersion2 {
		// DialTLSContext bypasses x/net/http2's own ALPN check, so the conn we
		// hand back may come from uTLS or REALITY without further ceremony.
		d.download = &http.Client{Transport: &http2.Transport{
			DialTLSContext: func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
				return dial(ctx)
			},
			IdleConnTimeout: idleConnTimeout,
			ReadIdleTimeout: h2KeepAlive,
			// Only consulted when Scheme is "http": prior-knowledge h2c, which
			// a plaintext-behind-CDN profile can legitimately ask for. Under
			// the usual https scheme this flag does nothing.
			AllowHTTP: true,
		}}
		return d, nil
	}

	httpDial := func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) }
	d.download = &http.Client{Transport: &http.Transport{
		DialContext:     httpDial,
		DialTLSContext:  httpDial,
		IdleConnTimeout: idleConnTimeout,
		// A chunked download with keep-alives is buggy with a custom dial
		// context (Xray hits the same thing): the pooled connection outlives
		// the stream and the next request reads leftover body bytes.
		DisableKeepAlives: true,
	}}
	d.upload = &http.Client{Transport: &http.Transport{
		DialContext:         httpDial,
		DialTLSContext:      httpDial,
		IdleConnTimeout:     idleConnTimeout,
		MaxIdleConnsPerHost: 8,
	}}
	return d, nil
}

// Close releases idle connections held by the dialer's HTTP clients. Streams
// already open keep working until they are closed individually.
func (d *Dialer) Close() error {
	for _, c := range []*http.Client{d.download, d.upload} {
		if c == nil {
			continue
		}
		if t, ok := c.Transport.(interface{ CloseIdleConnections() }); ok {
			t.CloseIdleConnections()
		}
	}
	return nil
}

// Mode returns the resolved transport mode (never "auto"), for logging.
func (d *Dialer) Mode() string { return d.mode }

// resolveMode turns "" / "auto" into a concrete mode the way Xray does:
// packet-up normally, stream-one under REALITY (where the client owns the whole
// TLS session anyway, so one full-duplex request is both safe and cheapest).
func resolveMode(mode string, reality bool) string {
	switch mode {
	case ModePacketUp, ModeStreamUp, ModeStreamOne:
		return mode
	default:
		if reality {
			return ModeStreamOne
		}
		return ModePacketUp
	}
}

// splitPathQuery splits the link's `path=` into a normalized path (leading and
// trailing slash, as the server matches on the prefix and then splits the
// remainder into segments) and its optional query string.
func splitPathQuery(raw string) (path, query string) {
	path, query, _ = strings.Cut(raw, "?")
	if path == "" || path[0] != '/' {
		path = "/" + path
	}
	if path[len(path)-1] != '/' {
		path += "/"
	}
	return path, query
}

// DialContext opens one XHTTP stream and returns it as a net.Conn.
func (d *Dialer) DialContext(ctx context.Context) (net.Conn, error) {
	sessionID := ""
	if d.mode != ModeStreamOne {
		sessionID = newSessionID()
	}

	uplinkReader, uplinkWriter := io.Pipe()
	conn := &streamConn{writer: uplinkWriter}

	switch d.mode {
	case ModeStreamOne:
		// One request carries both directions: the request body is the uplink,
		// the response body the downlink. Needs a full-duplex path (HTTP/2, or
		// HTTP/1.1 straight to the server with no buffering proxy).
		rc, remote, local, err := d.openStream(ctx, d.download, sessionID, uplinkReader, waitResponseBrief)
		if err != nil {
			uplinkWriter.CloseWithError(err)
			return nil, err
		}
		conn.reader, conn.remoteAddr, conn.localAddr = rc, remote, local
		return conn, nil

	case ModeStreamUp:
		rc, remote, local, err := d.openStream(ctx, d.download, sessionID, nil, waitResponse)
		if err != nil {
			uplinkWriter.CloseWithError(err)
			return nil, err
		}
		conn.reader, conn.remoteAddr, conn.localAddr = rc, remote, local
		// The uplink is a second, upload-only request with the same session id
		// and a streaming body; its response is discarded.
		if _, _, _, err := d.openStream(ctx, d.download, sessionID, uplinkReader, waitConnected); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil

	default: // ModePacketUp
		// The uplink is not a request body here, so the pipe is unused.
		uplinkWriter.Close()
		uplinkReader.Close()
		rc, remote, local, err := d.openStream(ctx, d.download, sessionID, nil, waitResponse)
		if err != nil {
			return nil, err
		}
		conn.reader, conn.remoteAddr, conn.localAddr = rc, remote, local
		conn.writer = newPacketUploader(ctx, d, sessionID)
		return conn, nil
	}
}

// newSessionID returns a random session identifier. Xray uses a UUID string;
// any opaque token works as long as it survives a URL path segment.
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// newRequest builds a request against the base URL with padding and metadata
// applied in the order the server expects.
func (d *Dialer) newRequest(ctx context.Context, method string, body io.Reader, sessionID, seq string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, d.baseURL.String(), body)
	if err != nil {
		return nil, err
	}
	req.Host = d.cfg.Authority
	req.Header = d.header()
	// Padding first (it snapshots the URL for the Referer placement), metadata
	// second (it extends the path with the session id and sequence number).
	d.settings.applyPadding(req)
	d.settings.applyMeta(req, sessionID, seq)
	return req, nil
}

func (d *Dialer) header() http.Header {
	h := make(http.Header, len(d.settings.Headers)+2)
	for k, v := range d.settings.Headers {
		h.Set(k, v)
	}
	if h.Get("User-Agent") == "" {
		ua := d.cfg.UserAgent
		if ua == "" {
			ua = defaultUserAgent
		}
		h.Set("User-Agent", ua)
	}
	return h
}

// waitMode says how long openStream blocks before handing the stream back.
type waitMode int

const (
	// waitResponse blocks until the response headers arrive (or the request
	// fails), so a wrong path, a stale key or bad padding surfaces as a dial
	// error instead of a stream that silently never carries anything.
	waitResponse waitMode = iota
	// waitResponseBrief is waitResponse for stream-one, where the response and
	// the request body share one request: a buffering middlebox may withhold
	// the response until the client has written something, so give up sooner.
	waitResponseBrief
	// waitConnected returns as soon as the connection is up. Used for the
	// upload-only half of stream-up, whose response only completes when the
	// whole stream does.
	waitConnected
)

// Grace periods bounding the header wait. They only ever elapse when the peer
// is withholding response headers — a healthy server answers within one round
// trip, and a broken one answers with a non-200 just as fast. Hitting the cap
// means proceeding optimistically: whatever is wrong then surfaces as a read
// error, which is still better than a dial that never returns.
const (
	responseGrace      = 10 * time.Second
	responseBriefGrace = 3 * time.Second
)

func (w waitMode) grace() time.Duration {
	if w == waitResponseBrief {
		return responseBriefGrace
	}
	return responseGrace
}

// openStream issues the long-lived request that carries one direction of the
// stream: a GET for the downlink, or the configured uplink method when body is
// non-nil.
func (d *Dialer) openStream(ctx context.Context, client *http.Client, sessionID string, body io.Reader, wait waitMode) (io.ReadCloser, net.Addr, net.Addr, error) {
	method := "GET"
	if body != nil {
		method = d.settings.uplinkMethod()
	}

	var remoteAddr, localAddr net.Addr
	connected := make(chan struct{})
	var connectedOnce sync.Once
	responded := make(chan struct{})
	failed := make(chan error, 1)

	traceCtx := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			remoteAddr, localAddr = info.Conn.RemoteAddr(), info.Conn.LocalAddr()
			connectedOnce.Do(func() { close(connected) })
		},
	})
	// The request must outlive the dial context — cancelling ctx after
	// DialContext returns would tear down a perfectly healthy stream — but it
	// must still be cancellable, which is what closing the stream does.
	reqCtx, cancelReq := context.WithCancel(context.WithoutCancel(traceCtx))
	req, err := d.newRequest(reqCtx, method, body, sessionID, "")
	if err != nil {
		cancelReq()
		return nil, nil, nil, err
	}
	if body != nil && !d.settings.NoGRPCHeader {
		// Makes the long-lived upload look like a gRPC stream to middleboxes,
		// which is what stops CDNs from buffering it.
		req.Header.Set("Content-Type", "application/grpc")
	}

	rc := &pendingReader{ready: make(chan struct{}), cancel: cancelReq}
	go func() {
		resp, err := client.Do(req)
		if err != nil {
			cancelReq()
			rc.fail(err)
			failed <- err
			return
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			cancelReq()
			err := fmt.Errorf("xhttp: %s %s: unexpected status %s", method, req.URL.Path, resp.Status)
			rc.fail(err)
			failed <- err
			return
		}
		if wait == waitConnected {
			// The upload-only response body carries nothing but keep-alive
			// padding; drain it so the connection is released on close.
			close(responded)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			cancelReq()
			rc.fail(io.EOF)
			return
		}
		rc.set(resp.Body)
		close(responded)
		connectedOnce.Do(func() { close(connected) })
	}()

	// Wait for whichever outcome this mode calls for.
	if wait == waitConnected {
		select {
		case <-connected:
			return rc, remoteAddr, localAddr, nil
		case err := <-failed:
			return nil, nil, nil, err
		case <-ctx.Done():
			rc.Close()
			return nil, nil, nil, ctx.Err()
		}
	}
	// Start the grace clock only once the connection is up, so a slow TCP/TLS
	// handshake does not eat into it.
	var grace <-chan time.Time
	select {
	case <-connected:
		grace = time.After(wait.grace())
	case err := <-failed:
		return nil, nil, nil, err
	case <-ctx.Done():
		rc.Close()
		return nil, nil, nil, ctx.Err()
	}
	select {
	case <-responded:
		return rc, remoteAddr, localAddr, nil
	case err := <-failed:
		return nil, nil, nil, err
	case <-grace:
		return rc, remoteAddr, localAddr, nil
	case <-ctx.Done():
		rc.Close()
		return nil, nil, nil, ctx.Err()
	}
}

// postPacket uploads one numbered chunk (packet-up mode).
func (d *Dialer) postPacket(ctx context.Context, sessionID string, seq int64, payload []byte) error {
	client := d.upload
	if client == nil {
		client = d.download
	}
	req, err := d.newRequest(ctx, d.settings.uplinkMethod(), nil, sessionID, strconv.FormatInt(seq, 10))
	if err != nil {
		return err
	}
	d.settings.attachPacketPayload(req, payload)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("xhttp: upload seq %d: unexpected status %s", seq, resp.Status)
	}
	return nil
}

// attachPacketPayload puts the chunk where the server will look for it: the
// request body by default, or base64 spread across numbered headers/cookies
// when the profile hides the uplink that way.
func (s Settings) attachPacketPayload(req *http.Request, payload []byte) {
	switch s.uplinkDataPlacement() {
	case PlacementHeader:
		for i, chunk := range chunkEncoded(payload, s.uplinkChunkSize()) {
			req.Header.Set(fmt.Sprintf("%s-%d", s.UplinkDataKey, i), chunk)
		}
	case PlacementCookie:
		for i, chunk := range chunkEncoded(payload, s.uplinkChunkSize()) {
			req.AddCookie(&http.Cookie{Name: fmt.Sprintf("%s_%d", s.UplinkDataKey, i), Value: chunk})
		}
	default: // body (and Xray's "auto", which the client treats as body)
		req.Body = io.NopCloser(bytes.NewReader(payload))
		req.ContentLength = int64(len(payload))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(payload)), nil
		}
	}
}

// chunkEncoded base64url-encodes payload and splits it into randomly sized
// chunks small enough to survive per-header/per-cookie size limits.
func chunkEncoded(payload []byte, size Range) []string {
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	var out []string
	for len(encoded) > 0 {
		n := min(int(size.rand()), len(encoded))
		if n <= 0 {
			n = len(encoded)
		}
		out = append(out, encoded[:n])
		encoded = encoded[n:]
	}
	return out
}
