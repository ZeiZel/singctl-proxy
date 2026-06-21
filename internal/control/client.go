package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// request opens the control socket, sends one command line and returns the
// trimmed reply.
func request(socketPath, cmd string) (string, error) {
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, cmd+"\n"); err != nil {
		return "", err
	}
	data, err := io.ReadAll(conn)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// Stop asks the running instance to shut down.
func Stop(socketPath string) error {
	resp, err := request(socketPath, "STOP")
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
	resp, err := request(socketPath, "STATUS")
	if err != nil {
		return Status{}, err
	}
	var st Status
	if err := json.Unmarshal([]byte(resp), &st); err != nil {
		return Status{}, fmt.Errorf("bad status reply %q: %w", resp, err)
	}
	return st, nil
}
