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

// FastDeadline bounds a quick command — MODE, STATUS, KEYS-*, PROXY-*, and
// anything else not named below. These handlers do at most an in-memory
// state change or a bounded runtime.Manager transition (see
// runtime.DefaultTransitionTimeout), so they must return in well under a
// human-perceptible pause. Before per-command deadlines existed, every
// command shared one 3-minute value sized for subscription fetches — which
// meant a stuck MODE command (see F2) turned a should-be-15s failure into a
// 3-minute hang for every OTHER command queued behind the same daemon.
const FastDeadline = 15 * time.Second

// SlowDeadline bounds SUB-* and SYSPROXY-* commands, which can shell out to
// the network or to `networksetup`/`lsof`-equivalents.
//
// It MUST exceed the slowest such handler, or the daemon tears the connection
// down mid-command and the client reports a meaningless socket error instead
// of the real failure. That is not hypothetical: at 15s it silently truncated
// every subscription command, because fetching a subscription costs up to two
// 15s attempts (direct, then via the local proxy) and SUB-UPDATE budgets two
// minutes for a whole refresh — so an unreachable panel surfaced in the UI as
// "control socket connect failed" rather than "the panel did not answer".
//
// The exported name and the parity test in cmd/singctl exist to keep this
// relationship visible: lowering it below a handler's own budget reintroduces
// the same class of bug.
const SlowDeadline = 3 * time.Minute

// slowCommands is the set of commands that get SlowDeadline instead of
// FastDeadline. Keep this in sync with cmd/singctl/main.go's registerControl —
// SUB-* (subscription fetch/refresh) and SYSPROXY-* (networksetup/PAC
// diagnostics) are the only handlers that can legitimately take a while.
var slowCommands = map[string]bool{
	"SUB-LIST":        true,
	"SUB-ADD":         true,
	"SUB-REMOVE":      true,
	"SUB-UPDATE":      true,
	"SYSPROXY-STATUS": true,
	"SYSPROXY-SET":    true,
	"SYSPROXY-CONFIG": true,
	"SYSPROXY-IMPORT": true,
}

// DeadlineFor returns the deadline that applies to cmd (case-insensitive):
// SlowDeadline for SUB-*/SYSPROXY-*, FastDeadline for everything else
// (MODE, STATUS, KEYS-*, PROXY-*, and any command not listed above).
func DeadlineFor(cmd string) time.Duration {
	if slowCommands[strings.ToUpper(strings.TrimSpace(cmd))] {
		return SlowDeadline
	}
	return FastDeadline
}

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
	// NetextSupported/NetextAvailable surface the macOS transparent-proxy system
	// extension: Supported is true only on darwin (there is no such mechanism
	// elsewhere), Available additionally requires it to be installed+approved
	// (systemextensionsctl "activated enabled").
	NetextSupported bool `json:"netext_supported,omitempty"`
	NetextAvailable bool `json:"netext_available,omitempty"`
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
//
// The wire framing is exactly one line per request (see serve, which reads
// with bufio.Reader.ReadString('\n') and cuts on the first space): an
// argument containing a literal newline is truncated silently at the first
// '\n', with no error. JSON arguments (SETTINGS-SET, PROC-LAUNCH) are safe
// because json.Marshal never emits a raw newline inside a compact encoding —
// but anything else that can be multi-line (a WireGuard INI config, say)
// must NOT be passed as a plain argument. Give it its own command whose
// argument is base64 (StdEncoding) of the raw text instead (see
// KEYS-ADD-CONFIG in cmd/singctl/main.go) — base64 needs no escaping rules of
// its own and cannot collide with another command's argument framing, which
// a general escape/unescape pass over every argument could (it would trade a
// visible truncation bug for an invisible corruption of, say, SETTINGS-SET's
// JSON).
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
	// Bound reading the request line with FastDeadline: it's a few bytes, so
	// even the slowest legitimate command should have written it well within
	// that. Once the command is known, extend the deadline to match its own
	// budget (SlowDeadline for SUB-*/SYSPROXY-*) before running the handler.
	_ = conn.SetDeadline(time.Now().Add(FastDeadline))
	line, _ := bufio.NewReader(conn).ReadString('\n')
	cmd, arg, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
	cmd = strings.ToUpper(strings.TrimSpace(cmd))
	_ = conn.SetDeadline(time.Now().Add(DeadlineFor(cmd)))

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
