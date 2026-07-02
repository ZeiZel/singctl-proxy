package licensesrv

import (
	"sync"
	"time"
)

// MemStore is an in-memory Store for tests.
type MemStore struct {
	mu sync.Mutex
	m  map[string]Record
}

// NewMemStore returns an empty in-memory store.
func NewMemStore() *MemStore { return &MemStore{m: map[string]Record{}} }

func (s *MemStore) Put(r Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[r.Claims.ID] = r
	return nil
}

func (s *MemStore) Get(id string) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.m[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return r, nil
}

func (s *MemStore) List() ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, 0, len(s.m))
	for _, r := range s.m {
		out = append(out, r)
	}
	return out, nil
}

func (s *MemStore) SetStatus(id string, st Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.m[id]
	if !ok {
		return ErrNotFound
	}
	r.Status = st
	s.m[id] = r
	return nil
}

func (s *MemStore) BindDevice(id, deviceID, email string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.m[id]
	if !ok {
		return ErrNotFound
	}
	r.DeviceID = deviceID
	r.Email = email
	r.ActivatedAt = time.Now().Unix()
	s.m[id] = r
	return nil
}

func (s *MemStore) ResetDevice(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.m[id]
	if !ok {
		return ErrNotFound
	}
	r.DeviceID = ""
	r.ActivatedAt = 0
	r.Email = ""
	s.m[id] = r
	return nil
}
