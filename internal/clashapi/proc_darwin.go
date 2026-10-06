//go:build darwin

package clashapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

func NewProcessResolver() func(int) string { return func(int) string { return "" } }

const ownerInventoryLimit = 4096
const ownerInventoryByteLimit = 8 << 20

type ownerEntry struct {
	name  string
	pid   int
	token string
}
type ownerInventoryService struct {
	mu        sync.Mutex
	at        time.Time
	owners    map[string]ownerEntry
	inventory func(context.Context) ([]byte, error)
	identity  func(int) (string, bool)
	ttl       time.Duration
}

var darwinOwners = ownerInventoryService{inventory: readOwnerInventory, identity: processToken, ttl: 5 * time.Second}

// enrichOwners runs only for explicit Connections requests. The inventory cache
// is shared across short-lived API clients, and never invoked by the poller.
func (c *Client) enrichOwners(ctx context.Context, conns []Connection) {
	darwinOwners.enrich(ctx, conns)
}
func (s *ownerInventoryService) enrich(ctx context.Context, conns []Connection) {
	if len(conns) == 0 {
		return
	}
	s.mu.Lock()
	if s.owners == nil || time.Since(s.at) >= s.ttl {
		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		raw, err := s.inventory(bounded)
		cancel()
		if err != nil {
			s.mu.Unlock()
			return
		}
		s.owners = parseOwnerInventoryWithIdentity(string(raw), s.identity)
		s.at = time.Now()
	}
	owners := s.owners
	s.mu.Unlock()
	// One identity probe per PID, including the first request after inventory.
	validated := map[int]string{}
	for i := range conns {
		if conns[i].Metadata.Process != "" || conns[i].Metadata.ProcessPath != "" {
			continue
		}
		e, ok := owners[ownerKey(conns[i].Metadata)]
		if !ok || e.name == "" {
			continue
		}
		token, checked := validated[e.pid]
		if !checked {
			if current, alive := s.identity(e.pid); alive {
				token = current
			}
			validated[e.pid] = token
		}
		if token != "" && token == e.token {
			conns[i].Metadata.Process = e.name
		}
	}
}

type boundedInventory struct{ bytes.Buffer }

func (b *boundedInventory) Write(p []byte) (int, error) {
	if b.Len()+len(p) > ownerInventoryByteLimit {
		return 0, errors.New("owner inventory exceeds limit")
	}
	return b.Buffer.Write(p)
}
func readOwnerInventory(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-FpcPn", "-iTCP", "-iUDP")
	var output boundedInventory
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
func parseOwnerInventoryWithIdentity(raw string, identity func(int) (string, bool)) map[string]ownerEntry {
	owners := map[string]ownerEntry{}
	tokens := map[int]string{}
	pid, name, network := 0, "", ""
	for _, field := range strings.Split(raw, "\n") {
		if field == "" {
			continue
		}
		switch field[0] {
		case 'p':
			pid, _ = strconv.Atoi(field[1:])
			name = ""
			network = ""
		case 'c':
			name = field[1:]
		case 'P':
			network = strings.ToLower(field[1:])
		case 'n':
			if pid <= 0 || name == "" || (network != "tcp" && network != "udp") {
				continue
			}
			local := strings.SplitN(field[1:], "->", 2)[0]
			endpoint, ok := canonicalOwnerEndpoint(local)
			if !ok {
				continue
			}
			key := network + "|" + endpoint
			if _, exists := owners[key]; !exists && len(owners) >= ownerInventoryLimit {
				continue
			}
			token, checked := tokens[pid]
			if !checked && len(tokens) >= ownerInventoryLimit {
				continue
			}
			if !checked {
				if value, alive := identity(pid); alive {
					token = value
				}
				tokens[pid] = token
			}
			if token == "" {
				continue
			}
			candidate := ownerEntry{name: name, pid: pid, token: token}
			if previous, exists := owners[key]; exists && previous != candidate {
				owners[key] = ownerEntry{}
				continue
			}
			owners[key] = candidate
		}
	}
	return owners
}
func ownerKey(m Metadata) string {
	endpoint, ok := canonicalOwnerEndpoint(net.JoinHostPort(m.SourceIP, m.SourcePort))
	if !ok {
		return ""
	}
	return strings.ToLower(m.Network) + "|" + endpoint
}
func canonicalOwnerEndpoint(endpoint string) (string, bool) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return "", false
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return "", false
	}
	return net.JoinHostPort(ip.Unmap().String(), strconv.Itoa(n)), true
}
func processToken(pid int) (string, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(kp.Proc.P_pid) != pid {
		return "", false
	}
	return fmt.Sprintf("%d:%d:%d", pid, kp.Proc.P_starttime.Sec, kp.Proc.P_starttime.Usec), true
}
