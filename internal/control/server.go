package control

import (
	"bufio"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// connDeadline bounds a single request/response. It is generous because some
// commands (MODE vpn, SETTINGS-SET) trigger a sing-box core reload.
const connDeadline = 15 * time.Second

// Status is the STATUS payload reported over the control socket.
type Status struct {
	PID       int    `json:"pid"`
	Mode      string `json:"mode"`
	StartedAt string `json:"started_at"`
	// CiscoActive/ProxyBypass/PhysIface surface the Cisco-coexistence state: when
	// Cisco AnyConnect is up, ProxyBypass reports whether the proxy's egress is
	// pinned to PhysIface (the physical NIC) to bypass Cisco, vs. riding it.
	CiscoActive bool   `json:"cisco_active,omitempty"`
	ProxyBypass bool   `json:"proxy_bypass,omitempty"`
	PhysIface   string `json:"phys_iface,omitempty"`
}

// Traffic is the TRAFFIC payload: cumulative byte counters (running totals, not
// rates). A client samples it on an interval and charts the per-second deltas.
type Traffic struct {
	Up   int64 `json:"up"`
	Down int64 `json:"down"`
}

// HandlerFunc handles one command. arg is everything after the first token
// (possibly empty, possibly JSON). The returned reply is sent verbatim as one
// line; a non-nil err is sent as "ERR <err>".
type HandlerFunc func(arg string) (reply string, err error)

// Server is a Unix-socket command registry: a running instance registers
// handlers (STATUS, STOP, MODE, SETTINGS-*, KEYS-*) that a second invocation
// drives remotely.
type Server struct {
	path     string
	mu       sync.RWMutex
	handlers map[string]HandlerFunc
	ln       net.Listener
}

// NewServer builds a control server for socketPath. Register commands with Handle.
func NewServer(socketPath string) *Server {
	return &Server{path: socketPath, handlers: map[string]HandlerFunc{}}
}

// Handle registers fn for cmd (case-insensitive).
func (s *Server) Handle(cmd string, fn HandlerFunc) {
	s.mu.Lock()
	s.handlers[strings.ToUpper(cmd)] = fn
	s.mu.Unlock()
}

// Start removes any stale socket, begins listening, and serves in a goroutine.
func (s *Server) Start() error {
	_ = os.Remove(s.path) // clear a stale socket from a crashed run
	ln, err := net.Listen("unix", s.path)
	if err != nil {
		return err
	}
	s.ln = ln
	go s.acceptLoop()
	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go s.serve(conn)
	}
}

func (s *Server) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(connDeadline))
	line, _ := bufio.NewReader(conn).ReadString('\n')
	cmd, arg, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
	cmd = strings.ToUpper(strings.TrimSpace(cmd))

	s.mu.RLock()
	h := s.handlers[cmd]
	s.mu.RUnlock()
	if h == nil {
		_, _ = io.WriteString(conn, "ERR unknown command\n")
		return
	}
	reply, err := h(arg)
	if err != nil {
		_, _ = io.WriteString(conn, "ERR "+err.Error()+"\n")
		return
	}
	_, _ = io.WriteString(conn, reply+"\n")
}

// Close stops listening and removes the socket file.
func (s *Server) Close() error {
	if s.ln == nil {
		return nil
	}
	err := s.ln.Close()
	_ = os.Remove(s.path)
	return err
}
