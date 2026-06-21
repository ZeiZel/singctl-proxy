package control

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// Status is the live status reported over the control socket.
type Status struct {
	PID       int    `json:"pid"`
	Mode      string `json:"mode"`
	StartedAt string `json:"started_at"`
}

// Server listens on a Unix socket and answers STATUS / STOP commands.
type Server struct {
	path   string
	status func() Status
	onStop func()
	ln     net.Listener
}

// NewServer builds a control server for socketPath. status reports current
// state; onStop is invoked (asynchronously) when a STOP command arrives.
func NewServer(socketPath string, status func() Status, onStop func()) *Server {
	return &Server{path: socketPath, status: status, onStop: onStop}
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
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	line, _ := bufio.NewReader(conn).ReadString('\n')
	switch strings.ToUpper(strings.TrimSpace(line)) {
	case "STOP":
		_, _ = io.WriteString(conn, "OK\n")
		if s.onStop != nil {
			go s.onStop()
		}
	case "STATUS":
		var st Status
		if s.status != nil {
			st = s.status()
		}
		data, _ := json.Marshal(st)
		_, _ = conn.Write(append(data, '\n'))
	default:
		_, _ = io.WriteString(conn, "ERR unknown command\n")
	}
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
