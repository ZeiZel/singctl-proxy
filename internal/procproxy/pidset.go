package procproxy

import "sync"

// muList is a small concurrency-safe ordered set of PIDs (insertion order
// preserved for stable display).
type muList struct {
	mu   sync.Mutex
	pids []int
}

func (m *muList) add(pid int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.pids {
		if p == pid {
			return
		}
	}
	m.pids = append(m.pids, pid)
}

func (m *muList) remove(pid int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, p := range m.pids {
		if p == pid {
			m.pids = append(m.pids[:i], m.pids[i+1:]...)
			return
		}
	}
}

func (m *muList) list() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int, len(m.pids))
	copy(out, m.pids)
	return out
}

// muSetup is a concurrency-safe one-shot "setup done" latch.
type muSetup struct {
	mu sync.Mutex
	ok bool
}

func (s *muSetup) done() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ok
}

func (s *muSetup) markDone() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ok = true
}
