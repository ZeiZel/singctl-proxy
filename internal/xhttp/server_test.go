package xhttp

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/http2"
)

// testServer is a minimal but faithful XHTTP server: it enforces exactly the
// checks a real Xray listener enforces (path prefix, padding length, session
// and sequence placement) and echoes every uplink byte back down the downlink.
// If our client drifts from the wire format, these tests fail the same way a
// real server would — with a 400/404 instead of a stream.
type testServer struct {
	path        string
	paddingFrom int32
	paddingTo   int32

	mu       sync.Mutex
	sessions map[string]*testSession
}

type testSession struct {
	upW   *io.PipeWriter
	downR *io.PipeReader

	mu      sync.Mutex
	next    uint64
	pending map[uint64][]byte
}

func newTestServer(path string) *testServer {
	return &testServer{path: path, paddingFrom: 100, paddingTo: 1000, sessions: map[string]*testSession{}}
}

// newSession wires an echo service: everything pushed into the uplink comes
// back out of the downlink.
func newTestSession() *testSession {
	upR, upW := io.Pipe()
	downR, downW := io.Pipe()
	go func() {
		io.Copy(downW, upR)
		downW.Close()
	}()
	return &testSession{upW: upW, downR: downR, pending: map[uint64][]byte{}}
}

// push stores an out-of-order chunk and flushes whatever prefix is now
// contiguous, exactly like the real server's upload queue.
func (s *testSession) push(seq uint64, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[seq] = payload
	for {
		chunk, ok := s.pending[s.next]
		if !ok {
			return nil
		}
		delete(s.pending, s.next)
		s.next++
		if _, err := s.upW.Write(chunk); err != nil {
			return err
		}
	}
}

func (t *testServer) session(id string) *testSession {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.sessions[id]
	if !ok {
		s = newTestSession()
		t.sessions[id] = s
	}
	return s
}

func (t *testServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, t.path) {
		http.Error(w, "bad path", http.StatusNotFound)
		return
	}
	if !t.paddingOK(r) {
		http.Error(w, "bad padding", http.StatusBadRequest)
		return
	}

	var sessionID, seqStr string
	rest := strings.Split(strings.TrimPrefix(r.URL.Path, t.path), "/")
	if len(rest) > 0 {
		sessionID = rest[0]
	}
	if len(rest) > 1 {
		seqStr = rest[1]
	}

	uplink := r.Method != "GET" || seqStr != ""

	switch {
	case uplink && sessionID != "" && seqStr == "": // stream-up
		s := t.session(sessionID)
		w.WriteHeader(http.StatusOK)
		flush(w)
		io.Copy(s.upW, r.Body)
		s.upW.Close()

	case uplink && sessionID != "": // packet-up
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read", http.StatusBadRequest)
			return
		}
		seq, err := strconv.ParseUint(seqStr, 10, 64)
		if err != nil {
			http.Error(w, "seq", http.StatusInternalServerError)
			return
		}
		if err := t.session(sessionID).push(seq, payload); err != nil {
			http.Error(w, "push", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)

	case sessionID == "": // stream-one: this one request is the whole stream
		s := newTestSession()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flush(w)
		go func() {
			io.Copy(s.upW, r.Body)
			s.upW.Close()
		}()
		copyFlushing(w, s.downR)

	default: // stream-down
		s := t.session(sessionID)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flush(w)
		copyFlushing(w, s.downR)
	}
}

// paddingOK mirrors the server-side padding check that rejects a client which
// forgets (or mis-sizes) its padding.
func (t *testServer) paddingOK(r *http.Request) bool {
	var value string
	if ref := r.Header.Get("Referer"); ref != "" {
		u, err := url.Parse(ref)
		if err != nil {
			return false
		}
		value = u.Query().Get("x_padding")
	} else {
		value = r.URL.Query().Get("x_padding")
	}
	n := int32(len(value))
	return n >= t.paddingFrom && n <= t.paddingTo
}

func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func copyFlushing(w http.ResponseWriter, r io.Reader) {
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			flush(w)
		}
		if err != nil {
			return
		}
	}
}

// serve starts the handler on a loopback listener and returns a dial function
// for Config.Dial plus a cleanup. h2 selects a raw HTTP/2 (prior-knowledge)
// server, matching what our client speaks once TLS has been established.
func serve(t *testing.T, h http.Handler, h2 bool) func(context.Context) (net.Conn, error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	if h2 {
		srv := &http2.Server{}
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				go srv.ServeConn(conn, &http2.ServeConnOpts{Handler: h})
			}
		}()
	} else {
		srv := &http.Server{Handler: h}
		go srv.Serve(ln)
		t.Cleanup(func() { srv.Close() })
	}

	addr := ln.Addr().String()
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", addr)
	}
}
