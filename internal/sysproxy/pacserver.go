package sysproxy

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

// pacServer is a tiny localhost HTTP server that serves the current PAC body
// at /proxy.pac. It replaces the Makefile's `pac-server` LaunchAgent
// (`python3 -m http.server`, installed separately and kept alive by
// launchd): the daemon already runs as a long-lived root process, so it can
// serve the PAC itself, in-process, with no separate agent to install or
// reload. Pure net/http — no os/exec, so it needs no darwin-only file and no
// entry in internal/arch/imports_test.go's exec allowlist.
type pacServer struct {
	mu   sync.Mutex
	srv  *http.Server
	port int
	body []byte
}

func newPACServer() *pacServer { return &pacServer{} }

// ensure starts the server if it isn't running yet, or restarts it if the
// caller asked for a different fixed port than the one it's currently bound
// to. port == 0 lets the OS assign one (used by tests) and, once a server is
// already up, is treated as "keep whatever's running" rather than rebinding a
// fresh ephemeral port on every call.
func (p *pacServer) ensure(port int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.srv != nil && (port == 0 || p.port == port) {
		return nil
	}
	if p.srv != nil {
		_ = p.srv.Close()
		p.srv = nil
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("sysproxy: pac server listen on %d: %w", port, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy.pac", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		body := p.body
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		_, _ = w.Write(body)
	})
	srv := &http.Server{Handler: mux}
	p.srv = srv
	p.port = ln.Addr().(*net.TCPAddr).Port
	go func() { _ = srv.Serve(ln) }()
	return nil
}

// setBody replaces the PAC content served at /proxy.pac.
func (p *pacServer) setBody(body []byte) {
	p.mu.Lock()
	p.body = body
	p.mu.Unlock()
}

// url returns the PAC's http:// URL, or "" if the server has never started
// (Config.Mode == ModeOff and Apply has never run in a non-off mode).
func (p *pacServer) url() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.srv == nil {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d/proxy.pac", p.port)
}

// answers reports whether the PAC server is up and answering — the same
// liveness check the Makefile's `ensure_pac_server` makes with curl.
func (p *pacServer) answers() bool {
	u := p.url()
	if u == "" {
		return false
	}
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(u)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// close shuts the server down, if running. Best-effort; safe to call
// multiple times.
func (p *pacServer) close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.srv == nil {
		return nil
	}
	err := p.srv.Close()
	p.srv = nil
	return err
}
