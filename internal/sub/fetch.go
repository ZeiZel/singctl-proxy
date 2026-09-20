// Package sub — this file is the network half: HTTPFetcher, the production
// implementation of the Fetcher interface declared in record.go. See sub.go
// for the pure parsing this builds on.
package sub

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// userAgent is sent on every subscription request. Panels (3x-ui, Marzban,
// Remnawave, and friends) commonly branch their response on the client's
// User-Agent: a browser or unrecognised UA is served a human-readable web
// page (sometimes even a styled HTML error page), while a client UA gets the
// actual machine-readable link list. v2rayNG is the most common Android
// client and effectively the de-facto UA subscription panels are built
// against, so spoofing it — rather than sending Go's default UA, or none —
// is what makes fetching work against panels that behave this way at all.
const userAgent = "v2rayNG/1.9.0"

// maxBodySize caps how much of a response body Fetch will read. A subscription
// body, even decoded and with a few hundred servers, is well under a
// megabyte; a wrong or hijacked URL that serves an oversized page (or an
// infinite stream) must not be able to turn a routine refresh into a memory
// exhaustion. The cap is enforced with one byte of slack so it can tell
// "exactly at the limit" apart from "over the limit".
const maxBodySize = 4 << 20 // 4 MiB

// AttemptTimeout bounds a single HTTP attempt (direct or via proxy),
// independently of whatever deadline the caller's context carries. Bounding
// each attempt individually — rather than splitting the caller's deadline in
// two — is what keeps the direct→proxy fallback's worst case predictable: at
// most AttemptTimeout twice, regardless of what the caller passed in.
// AttemptTimeout bounds ONE fetch attempt. Fetch makes at most two (direct,
// then via the local proxy), so a whole fetch costs at most twice this — a
// budget the control socket's ConnDeadline has to stay above.
const AttemptTimeout = 15 * time.Second

// doer is the minimal subset of *http.Client that HTTPFetcher needs. Keeping
// it as an interface — rather than depending on *http.Client concretely —
// lets tests substitute a stub transport without a real network, and keeps
// this package from dictating how the caller configures its client (proxying
// notwithstanding, see NewHTTPFetcher).
type doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// HTTPFetcher is the production Fetcher: it fetches a subscription URL over
// HTTP(S), first directly and — only if that fails — through the local proxy
// the daemon itself is running.
//
// The direct-first order is deliberate and must not be reversed: the whole
// point of singctl is that the network it runs on may be blocking arbitrary
// hosts, including the subscription panel's, so a refresh that depended on
// the proxy tunnel being up would fail exactly when the user most needs it —
// right after the tunnel itself went down. Direct-first means a healthy
// network path still works even if the proxy is misconfigured or its
// upstream server is unreachable; proxy-as-fallback means a blocked panel
// still resolves as long as the tunnel is up.
type HTTPFetcher struct {
	client    doer   // used for the direct attempt
	proxyAddr string // "host:port" of the local proxy; empty disables the fallback
}

// NewHTTPFetcher builds an HTTPFetcher.
//
// client is used for the direct attempt; pass nil for a sane default (a
// plain *http.Client with AttemptTimeout and the standard follow-redirects
// policy). A caller with an existing *http.Client can pass it directly, since
// *http.Client satisfies doer.
//
// proxyAddr is the daemon's own local HTTP proxy listen address (e.g.
// "127.0.0.1:2080", see procproxy.defaultHTTPAddr) used ONLY as a fallback
// when the direct attempt fails. Pass "" to disable the fallback entirely,
// e.g. when the caller has no local proxy running yet.
func NewHTTPFetcher(client doer, proxyAddr string) *HTTPFetcher {
	if client == nil {
		// The direct attempt has to be GENUINELY direct. http.DefaultTransport
		// honours HTTP(S)_PROXY, and on the machines singctl runs on those
		// variables point at singctl's own local proxy — so "direct" would go
		// through the very tunnel the fallback exists to work around, and both
		// attempts would fail together whenever the daemon happens to be down.
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		client = &http.Client{Timeout: AttemptTimeout, Transport: transport}
	}
	return &HTTPFetcher{client: client, proxyAddr: strings.TrimSpace(proxyAddr)}
}

// Fetch retrieves and parses a subscription, using the default v2rayNG
// User-Agent (see the userAgent constant's doc comment for why that one).
// It tries the URL directly; if that fails (transport error or non-2xx
// status) and a proxy address was configured, it retries once through the
// local proxy. If both attempts fail, the returned error names both routes
// and wraps both underlying errors, so a log line alone is enough to tell
// "direct network problem", "proxy misconfigured", and "panel actually
// down" apart.
func (f *HTTPFetcher) Fetch(ctx context.Context, rawURL string) (Result, error) {
	return f.fetch(ctx, rawURL, userAgent)
}

// FetchWithUserAgent is Fetch but with the request's User-Agent overridden.
// The default v2rayNG UA already gets a link list out of every real panel
// this has been tested against, so ordinary callers want Fetch, not this —
// it exists for the rarer panel that branches its RESPONSE FORMAT on the
// exact client UA string (not just "known VPN client or not") and needs a
// specific one to serve something machine-readable at all. An empty ua
// falls back to the default, same as calling Fetch.
func (f *HTTPFetcher) FetchWithUserAgent(ctx context.Context, rawURL, ua string) (Result, error) {
	if strings.TrimSpace(ua) == "" {
		ua = userAgent
	}
	return f.fetch(ctx, rawURL, ua)
}

// fetch is Fetch's actual implementation, parameterized on the User-Agent so
// FetchWithUserAgent can share it without duplicating the direct→proxy
// fallback logic.
func (f *HTTPFetcher) fetch(ctx context.Context, rawURL, ua string) (Result, error) {
	body, header, directErr := f.attempt(ctx, rawURL, f.client, ua)
	if directErr == nil {
		return f.toResult(body, header)
	}

	if f.proxyAddr == "" {
		return Result{}, fmt.Errorf("sub: fetch %s: %w", rawURL, directErr)
	}

	proxyClient, buildErr := f.proxiedClient()
	if buildErr != nil {
		return Result{}, fmt.Errorf("sub: fetch %s: direct attempt failed (%v), and the proxy client could not be built: %w", rawURL, directErr, buildErr)
	}

	body, header, proxyErr := f.attempt(ctx, rawURL, proxyClient, ua)
	if proxyErr != nil {
		return Result{}, fmt.Errorf("sub: fetch %s: tried direct and via proxy %s, both failed — direct: %w; proxy: %w", rawURL, f.proxyAddr, directErr, proxyErr)
	}
	return f.toResult(body, header)
}

// attempt performs one GET, with its own bounded timeout layered on top of
// ctx, and returns the (capped) body and response headers on success.
func (f *HTTPFetcher) attempt(ctx context.Context, rawURL string, client doer, ua string) ([]byte, http.Header, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, AttemptTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", ua)

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The status alone (plus the URL, added by the caller) must be enough
		// to diagnose a stale token or a panel that has revoked a subscription
		// from a log line, without needing to reproduce the request by hand.
		return nil, nil, fmt.Errorf("unexpected status %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read body: %w", err)
	}
	if len(body) > maxBodySize {
		return nil, nil, fmt.Errorf("response body exceeds %d byte cap (wrong URL?)", maxBodySize)
	}
	return body, resp.Header, nil
}

// proxiedClient builds a client that routes every request through the
// configured local proxy, independently of f.client's own transport — the
// fallback must work even when f.client was constructed with a transport
// that has nothing to do with proxying.
func (f *HTTPFetcher) proxiedClient() (*http.Client, error) {
	addr := f.proxyAddr
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	proxyURL, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy address %q: %w", f.proxyAddr, err)
	}
	return &http.Client{
		Timeout:   AttemptTimeout,
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}, nil
}

// toResult parses a fetched body and its headers into a Result, delegating
// entirely to the pure parsing in sub.go.
func (f *HTTPFetcher) toResult(body []byte, header http.Header) (Result, error) {
	links, err := Parse(body)
	if err != nil {
		return Result{}, err
	}
	return Result{Links: links, Meta: ParseMeta(header)}, nil
}

var _ Fetcher = (*HTTPFetcher)(nil)
