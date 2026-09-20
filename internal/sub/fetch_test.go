package sub

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestHTTPFetcher_HappyPath(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Subscription-Userinfo", "upload=111; download=222; total=333; expire=1735689600")
		w.Header().Set("Profile-Title", "My Panel")
		w.Header().Set("Profile-Update-Interval", "6")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(link1 + "\n" + link3))
	}))
	defer srv.Close()

	f := NewHTTPFetcher(nil, "") // no proxy configured — must not be needed on the happy path
	got, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if len(got.Links) != 2 || got.Links[0] != link1 || got.Links[1] != link3 {
		t.Errorf("Links = %#v, want [%s %s]", got.Links, link1, link3)
	}
	if got.Meta.Title != "My Panel" || got.Meta.Upload != 111 || got.Meta.Download != 222 || got.Meta.Total != 333 {
		t.Errorf("Meta = %+v", got.Meta)
	}
	if gotUA != userAgent {
		t.Errorf("User-Agent = %q, want %q (panels branch their response on it)", gotUA, userAgent)
	}
}

// TestHTTPFetcher_FetchWithUserAgent proves the override actually reaches
// the request, and that Fetch itself is unaffected — the default UA some
// panels depend on must never change just because this method exists.
func TestHTTPFetcher_FetchWithUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(link1))
	}))
	defer srv.Close()

	f := NewHTTPFetcher(nil, "")
	const customUA = "ClashMetaForAndroid/2.11.10"
	if _, err := f.FetchWithUserAgent(context.Background(), srv.URL, customUA); err != nil {
		t.Fatalf("FetchWithUserAgent: %v", err)
	}
	if gotUA != customUA {
		t.Errorf("User-Agent = %q, want the override %q", gotUA, customUA)
	}

	if _, err := f.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotUA != userAgent {
		t.Errorf("Fetch's User-Agent = %q, want the unchanged default %q", gotUA, userAgent)
	}
}

// TestHTTPFetcher_FetchWithUserAgent_EmptyFallsBackToDefault checks that an
// empty override is treated as "no override" rather than sending a blank
// User-Agent header.
func TestHTTPFetcher_FetchWithUserAgent_EmptyFallsBackToDefault(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(link1))
	}))
	defer srv.Close()

	f := NewHTTPFetcher(nil, "")
	if _, err := f.FetchWithUserAgent(context.Background(), srv.URL, ""); err != nil {
		t.Fatalf("FetchWithUserAgent: %v", err)
	}
	if gotUA != userAgent {
		t.Errorf("User-Agent = %q, want the default %q", gotUA, userAgent)
	}
}

func TestHTTPFetcher_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "token revoked", http.StatusNotFound)
	}))
	defer srv.Close()

	f := NewHTTPFetcher(nil, "")
	_, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error %q must mention the status code so a 404 on a stale token is diagnosable from the log alone", err.Error())
	}
}

func TestHTTPFetcher_BodyCap(t *testing.T) {
	oversized := strings.Repeat("x", maxBodySize+1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(oversized))
	}))
	defer srv.Close()

	f := NewHTTPFetcher(nil, "")
	_, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected an error: response body exceeds the cap")
	}
}

// TestHTTPFetcher_DirectFirst_NoProxyOnSuccess proves the proxy is never
// touched when the direct attempt succeeds — going through the proxy first,
// or even as a needless double-check, would make a healthy direct network
// path depend on the tunnel being up too.
func TestHTTPFetcher_DirectFirst_NoProxyOnSuccess(t *testing.T) {
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(link1))
	}))
	defer direct.Close()

	var proxyHits int
	var mu sync.Mutex
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		proxyHits++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(link1))
	}))
	defer proxy.Close()

	f := NewHTTPFetcher(nil, strings.TrimPrefix(proxy.URL, "http://"))
	if _, err := f.Fetch(context.Background(), direct.URL); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if proxyHits != 0 {
		t.Errorf("proxy was hit %d times, want 0 — direct succeeded and the fallback must not fire", proxyHits)
	}
}

// TestHTTPFetcher_FallsBackToProxyAfterDirectFails is the important case: the
// direct attempt must be tried FIRST, and the proxy only used once direct has
// actually failed. Reversing this order would make a routine subscription
// refresh depend on a tunnel that may itself be down.
func TestHTTPFetcher_FallsBackToProxyAfterDirectFails(t *testing.T) {
	var mu sync.Mutex
	var order []string

	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		order = append(order, "direct")
		mu.Unlock()
		http.Error(w, "upstream blocked", http.StatusServiceUnavailable)
	}))
	defer direct.Close()

	var proxyReqTarget string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		order = append(order, "proxy")
		proxyReqTarget = r.URL.String()
		if r.URL.Host == "" {
			// Fall back to the Host header if the request line came in
			// origin-form rather than absolute-form for some reason.
			proxyReqTarget = r.Host + r.URL.Path
		}
		mu.Unlock()
		w.Header().Set("Profile-Title", "Via Proxy")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(link3))
	}))
	defer proxy.Close()

	f := NewHTTPFetcher(nil, strings.TrimPrefix(proxy.URL, "http://"))
	got, err := f.Fetch(context.Background(), direct.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(order) != 2 || order[0] != "direct" || order[1] != "proxy" {
		t.Fatalf("call order = %v, want [direct proxy] (direct must be tried first)", order)
	}
	if !strings.Contains(proxyReqTarget, strings.TrimPrefix(direct.URL, "http://")) {
		t.Errorf("proxy received target %q, want it to reference the direct URL %q (proving it went THROUGH the proxy to the original target)", proxyReqTarget, direct.URL)
	}
	if len(got.Links) != 1 || got.Links[0] != link3 {
		t.Errorf("Links = %#v, want [%s] (from the proxy response)", got.Links, link3)
	}
	if got.Meta.Title != "Via Proxy" {
		t.Errorf("Meta.Title = %q, want the proxy response's headers to be the ones parsed", got.Meta.Title)
	}
}

// TestHTTPFetcher_BothRoutesFail_ErrorNamesBoth ensures a total failure is
// diagnosable: the error must say both routes were tried and carry both
// underlying errors, so a log line alone tells apart "network is fine but the
// panel is down" from "proxy itself is broken".
func TestHTTPFetcher_BothRoutesFail_ErrorNamesBoth(t *testing.T) {
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "direct blocked", http.StatusServiceUnavailable)
	}))
	defer direct.Close()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "proxy also failing", http.StatusBadGateway)
	}))
	defer proxy.Close()

	f := NewHTTPFetcher(nil, strings.TrimPrefix(proxy.URL, "http://"))
	_, err := f.Fetch(context.Background(), direct.URL)
	if err == nil {
		t.Fatal("expected an error when both routes fail")
	}
	msg := err.Error()
	for _, want := range []string{"direct", "proxy", "503", "502"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q must mention %q so both failures are diagnosable from the log alone", msg, want)
		}
	}
}

// TestHTTPFetcher_NoProxyConfigured_ErrorFromDirectOnly checks that when no
// proxy address was given, a direct failure surfaces immediately without
// implying a fallback was attempted.
func TestHTTPFetcher_NoProxyConfigured_ErrorFromDirectOnly(t *testing.T) {
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusGone)
	}))
	defer direct.Close()

	f := NewHTTPFetcher(nil, "")
	_, err := f.Fetch(context.Background(), direct.URL)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "via proxy") {
		t.Errorf("error %q must not claim a proxy fallback was attempted when none was configured", err.Error())
	}
}

// TestHTTPFetcher_CustomDoer verifies the doer interface accepts something
// other than *http.Client, and that a transport-level error (as opposed to a
// non-2xx status) also triggers the proxy fallback.
func TestHTTPFetcher_CustomDoer(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(link1))
	}))
	defer proxy.Close()

	stub := stubDoer{err: fmt.Errorf("connection refused (simulated)")}
	f := NewHTTPFetcher(stub, strings.TrimPrefix(proxy.URL, "http://"))
	// http (not https): an http:// target routed through a proxy is sent to
	// the proxy verbatim in absolute-URI form, so the proxy stub answers
	// without ever needing to actually resolve/dial blocked.invalid.example.
	// https would require a real CONNECT tunnel to a resolvable host, which
	// this test deliberately avoids depending on.
	got, err := f.Fetch(context.Background(), "http://blocked.invalid.example/sub/abc")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Links) != 1 || got.Links[0] != link1 {
		t.Errorf("Links = %#v, want [%s]", got.Links, link1)
	}
}

type stubDoer struct {
	err error
}

func (s stubDoer) Do(*http.Request) (*http.Response, error) {
	return nil, s.err
}

// TestFetch_DirectAttemptIgnoresEnvironmentProxy pins the fix for a failure
// found against a real panel: the default client used http.DefaultTransport,
// which honours HTTP(S)_PROXY. On a machine whose HTTP_PROXY points at
// singctl's own listener, the "direct" attempt went through the tunnel and both
// routes failed together whenever the daemon was down — while plain curl
// reached the panel instantly.
func TestFetch_DirectAttemptIgnoresEnvironmentProxy(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte(
		"vless://4ce58870-27d3-489b-87a0-3109db4fb919@a.example.com:443?security=tls&sni=a.example.com#A"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	// A proxy address that nothing is listening on: if the direct attempt
	// honoured it, the fetch could not succeed.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")

	res, err := NewHTTPFetcher(nil, "").Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("direct fetch honoured the environment proxy: %v", err)
	}
	if len(res.Links) != 1 {
		t.Fatalf("links = %d, want 1", len(res.Links))
	}
}
