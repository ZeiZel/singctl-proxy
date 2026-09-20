package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Request sends one command (with an optional argument) to the control socket
// and returns the trimmed reply. A reply beginning with "ERR " becomes an error.
func Request(socketPath, cmd, arg string) (string, error) {
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(DeadlineFor(cmd)))

	line := cmd
	if arg != "" {
		line += " " + arg
	}
	if _, err := io.WriteString(conn, line+"\n"); err != nil {
		return "", err
	}
	data, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	reply := strings.TrimRight(string(data), "\r\n")
	if strings.HasPrefix(reply, "ERR ") {
		return "", fmt.Errorf("%s", strings.TrimPrefix(reply, "ERR "))
	}
	return reply, nil
}

// Stop asks the running instance to shut down.
func Stop(socketPath string) error {
	resp, err := Request(socketPath, "STOP", "")
	if err != nil {
		return err
	}
	if resp != "OK" {
		return fmt.Errorf("unexpected control reply: %q", resp)
	}
	return nil
}

// QueryStatus asks the running instance for its status.
func QueryStatus(socketPath string) (Status, error) {
	resp, err := Request(socketPath, "STATUS", "")
	if err != nil {
		return Status{}, err
	}
	var st Status
	if err := json.Unmarshal([]byte(resp), &st); err != nil {
		return Status{}, fmt.Errorf("bad status reply %q: %w", resp, err)
	}
	return st, nil
}

// QueryTraffic asks the running instance for its cumulative byte counters
// (TRAFFIC). Callers sample this on an interval to chart throughput.
func QueryTraffic(socketPath string) (Traffic, error) {
	resp, err := Request(socketPath, "TRAFFIC", "")
	if err != nil {
		return Traffic{}, err
	}
	var tr Traffic
	if err := json.Unmarshal([]byte(resp), &tr); err != nil {
		return Traffic{}, fmt.Errorf("bad traffic reply %q: %w", resp, err)
	}
	return tr, nil
}
