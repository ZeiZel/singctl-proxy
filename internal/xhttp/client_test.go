package xhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// dialStream opens one stream against a freshly started echo server.
func dialStream(t *testing.T, mode string, h2 bool, s Settings) net.Conn {
	t.Helper()
	srv := newTestServer("/xh/")
	dial := serve(t, srv, h2)

	version := HTTPVersion11
	if h2 {
		version = HTTPVersion2
	}
	s.Path = "/xh"
	s.Mode = mode

	d, err := New(Config{
		Dial:        dial,
		Scheme:      "http",
		Authority:   "example.com",
		HTTPVersion: version,
		Settings:    s,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx)
	if err != nil {
		t.Fatalf("DialContext(%s, h2=%v): %v", mode, h2, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// roundTrip writes payload and reads exactly len(payload) bytes back from the
// echo server, which proves the uplink framing, the session/sequence placement
// and the downlink all agree with the server's expectations.
func roundTrip(t *testing.T, conn net.Conn, payload []byte) {
	t.Helper()
	go func() {
		if _, err := conn.Write(payload); err != nil {
			t.Errorf("write: %v", err)
		}
	}()

	got := make([]byte, len(payload))
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(conn, got)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read back: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for the echoed payload")
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("echo mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

func TestRoundTripModes(t *testing.T) {
	payload := []byte(strings.Repeat("singctl-xhttp-payload;", 64))
	for _, tc := range []struct {
		name string
		mode string
		h2   bool
	}{
		{"packet-up/http1.1", ModePacketUp, false},
		{"packet-up/h2", ModePacketUp, true},
		{"stream-up/http1.1", ModeStreamUp, false},
		{"stream-up/h2", ModeStreamUp, true},
		{"stream-one/h2", ModeStreamOne, true},
		{"stream-one/http1.1", ModeStreamOne, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roundTrip(t, dialStream(t, tc.mode, tc.h2, Settings{}), payload)
		})
	}
}

// TestRoundTripLargePayload exercises the packet-up chunking path: the payload
// is several times scMaxEachPostBytes, so it must be split across sequenced
// POSTs and reassembled in order by the server.
func TestRoundTripLargePayload(t *testing.T) {
	payload := make([]byte, 300*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	s := Settings{
		ScMaxEachPostBytes:   Range{From: 64 * 1024, To: 64 * 1024},
		ScMinPostsIntervalMs: Range{From: 1, To: 1},
	}
	roundTrip(t, dialStream(t, ModePacketUp, true, s), payload)
}

// TestStreamingBothWays checks the stream stays open and ordered across several
// interleaved writes rather than one big burst.
func TestStreamingBothWays(t *testing.T) {
	conn := dialStream(t, ModeStreamUp, true, Settings{})
	for i := range 5 {
		msg := []byte(strings.Repeat("chunk", i+1))
		if _, err := conn.Write(msg); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		got := make([]byte, len(msg))
		if _, err := io.ReadFull(conn, got); err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if !bytes.Equal(got, msg) {
			t.Fatalf("chunk %d: got %q want %q", i, got, msg)
		}
	}
}

// TestPaddingIsSentAndSized guards the check that actually breaks connections
// in the field: the server rejects any request whose padding falls outside
// xPaddingBytes.
func TestPaddingIsSentAndSized(t *testing.T) {
	srv := newTestServer("/xh/")
	srv.paddingFrom, srv.paddingTo = 300, 400
	dial := serve(t, srv, true)

	d, err := New(Config{
		Dial: dial, Scheme: "http", Authority: "example.com", HTTPVersion: HTTPVersion2,
		Settings: Settings{Path: "/xh", Mode: ModePacketUp, XPaddingBytes: Range{From: 300, To: 400}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := d.DialContext(ctx)
	if err != nil {
		t.Fatalf("dial with in-range padding: %v", err)
	}
	conn.Close()

	// Now a client whose padding is far too short: the server must refuse it.
	bad, err := New(Config{
		Dial: dial, Scheme: "http", Authority: "example.com", HTTPVersion: HTTPVersion2,
		Settings: Settings{Path: "/xh", Mode: ModePacketUp, XPaddingBytes: Range{From: 1, To: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bad.Close() })
	if _, err := bad.DialContext(ctx); err == nil {
		t.Fatal("expected the server to reject undersized padding")
	}
}

// TestBadPathRejected proves a wrong path surfaces at dial time rather than as
// a silent stall — this is what a stale/mistyped key looks like.
func TestBadPathRejected(t *testing.T) {
	dial := serve(t, newTestServer("/right/"), true)
	d, err := New(Config{
		Dial: dial, Scheme: "http", Authority: "example.com", HTTPVersion: HTTPVersion2,
		Settings: Settings{Path: "/wrong", Mode: ModePacketUp},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := d.DialContext(ctx); err == nil {
		t.Fatal("expected a dial error for a mismatched path")
	}
}

func TestResolveMode(t *testing.T) {
	for _, tc := range []struct {
		in      string
		reality bool
		want    string
	}{
		{"", false, ModePacketUp},
		{ModeAuto, false, ModePacketUp},
		{"", true, ModeStreamOne},
		{ModeAuto, true, ModeStreamOne},
		{ModeStreamUp, true, ModeStreamUp},
		{ModePacketUp, true, ModePacketUp},
	} {
		if got := resolveMode(tc.in, tc.reality); got != tc.want {
			t.Errorf("resolveMode(%q, reality=%v) = %q, want %q", tc.in, tc.reality, got, tc.want)
		}
	}
}

func TestSplitPathQuery(t *testing.T) {
	for _, tc := range []struct{ in, path, query string }{
		{"", "/", ""},
		{"/xh", "/xh/", ""},
		{"xh", "/xh/", ""},
		{"/xh/", "/xh/", ""},
		{"/xh?ed=2560", "/xh/", "ed=2560"},
	} {
		path, query := splitPathQuery(tc.in)
		if path != tc.path || query != tc.query {
			t.Errorf("splitPathQuery(%q) = (%q, %q), want (%q, %q)", tc.in, path, query, tc.path, tc.query)
		}
	}
}

func TestParseSettings(t *testing.T) {
	s, err := ParseSettings(`{"scMaxEachPostBytes":"100000-200000","xPaddingBytes":500,"noGRPCHeader":true,"headers":{"X-Test":"1"}}`)
	if err != nil {
		t.Fatalf("ParseSettings: %v", err)
	}
	if s.ScMaxEachPostBytes != (Range{From: 100000, To: 200000}) {
		t.Errorf("scMaxEachPostBytes = %+v", s.ScMaxEachPostBytes)
	}
	if s.XPaddingBytes != (Range{From: 500, To: 500}) {
		t.Errorf("xPaddingBytes = %+v", s.XPaddingBytes)
	}
	if !s.NoGRPCHeader || s.Headers["X-Test"] != "1" {
		t.Errorf("unexpected settings: %+v", s)
	}
	if _, err := ParseSettings(`{`); err == nil {
		t.Error("expected an error for malformed extra")
	}
	if _, err := json.Marshal(s); err != nil {
		t.Fatal(err)
	}
}

func TestUnsupportedHTTP3(t *testing.T) {
	_, err := New(Config{
		Dial:   func(context.Context) (net.Conn, error) { return nil, nil },
		Scheme: "https", Authority: "example.com", HTTPVersion: HTTPVersion3,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP/3") {
		t.Fatalf("want an HTTP/3 rejection, got %v", err)
	}
}
