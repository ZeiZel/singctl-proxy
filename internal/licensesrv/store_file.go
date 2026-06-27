package licensesrv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// FileStore persists records as a single JSON file, written atomically
// (temp + rename) under an RWMutex. Adequate and reliable for a low-volume
// license server (mostly manual issuance); the whole DB is one backup-able file.
// Swap for bbolt/Postgres behind the Store interface if volume grows.
type FileStore struct {
	mu   sync.RWMutex
	path string
	data map[string]Record
}

// NewFileStore opens (or creates) the JSON store at path.
func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path, data: map[string]Record{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("licensesrv: open store %s: %w", path, err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, fmt.Errorf("licensesrv: parse store %s: %w", path, err)
		}
	}
	return s, nil
}

func (s *FileStore) Put(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[r.Claims.ID] = r
	return s.flushLocked()
}

func (s *FileStore) Get(id string) (Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.data[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return r, nil
}

func (s *FileStore) List() ([]Record, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, 0, len(s.data))
	for _, r := range s.data {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}

func (s *FileStore) SetStatus(id string, st Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.data[id]
	if !ok {
		return ErrNotFound
	}
	r.Status = st
	s.data[id] = r
	return s.flushLocked()
}

// flushLocked atomically rewrites the JSON file (caller holds the write lock).
func (s *FileStore) flushLocked() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
