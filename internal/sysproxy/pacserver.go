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
	mu      sync.Mutex
	srv     *http.Server
	port    int
	content *pacContent
}

type pacContent struct {
	mu   sync.RWMutex
	body []byte
}

type pacCandidate struct {
	srv     *http.Server
	port    int
	content *pacContent
}

// pacListen is a variable solely so lifecycle tests can model a bind conflict
// on the legacy port without relying on that real port being free on the test
// host. Production always uses net.Listen.
var pacListen = net.Listen

func newPACServer() *pacServer { return &pacServer{content: &pacContent{}} }

// prepare binds a candidate server without changing the currently committed
// listener. The caller commits it only after NetworkSetup accepts the new
// URL, or aborts it on any failure.
func (p *pacServer) prepare(port int, body []byte) (*pacCandidate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.srv != nil && (port == 0 || p.port == port) {
		return nil, nil
	}

	// Bind the replacement before retiring the current listener. A failed
	// rebind (for example, an imported config pinning a port held by another
	// process) must leave the currently applied PAC alive. Closing the old
	// listener first would turn a rejected Apply into a broken system proxy.
	ln, err := pacListen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("sysproxy: pac server listen on %d: %w", port, err)
	}
	content := &pacContent{body: append([]byte(nil), body...)}
	mux := http.NewServeMux()
	mux.HandleFunc("/proxy.pac", func(w http.ResponseWriter, r *http.Request) {
		content.mu.RLock()
		servedBody := append([]byte(nil), content.body...)
		content.mu.RUnlock()
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		_, _ = w.Write(servedBody)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return &pacCandidate{srv: srv, port: ln.Addr().(*net.TCPAddr).Port, content: content}, nil
}

// ensure retains the legacy internal helper semantics for package tests and
// callers that only need to make the current server listen. Manager.Apply
// uses prepare/commit so NetworkSetup failures can roll back safely.
func (p *pacServer) ensure(port int) error {
	body := p.bodySnapshot()
	c, err := p.prepare(port, body)
	if err != nil {
		return err
	}
	p.commit(c, body)
	return nil
}

func (p *pacServer) bodySnapshot() []byte {
	p.mu.Lock()
	content := p.content
	p.mu.Unlock()
	if content == nil {
		return nil
	}
	content.mu.RLock()
	defer content.mu.RUnlock()
	return append([]byte(nil), content.body...)
}

func (p *pacServer) candidateURL(c *pacCandidate) string {
	if c != nil {
		return fmt.Sprintf("http://127.0.0.1:%d/proxy.pac", c.port)
	}
	return p.url()
}

func (p *pacServer) commit(c *pacCandidate, body []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c != nil {
		old := p.srv
		p.srv, p.port = c.srv, c.port
		p.content = c.content
		if old != nil {
			_ = old.Close()
		}
	}
	if p.content == nil {
		p.content = &pacContent{}
	}
	p.content.mu.Lock()
	p.content.body = append(p.content.body[:0], body...)
	p.content.mu.Unlock()
}

func (p *pacServer) abort(c *pacCandidate) {
	if c != nil {
		_ = c.srv.Close()
	}
}

// setBody replaces the PAC content served at /proxy.pac.
func (p *pacServer) setBody(body []byte) {
	p.mu.Lock()
	if p.content == nil {
		p.content = &pacContent{}
	}
	content := p.content
	p.mu.Unlock()
	content.mu.Lock()
	content.body = append(content.body[:0], body...)
	content.mu.Unlock()
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
