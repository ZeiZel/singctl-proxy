package licensesrv

import (
	"path/filepath"
	"testing"

	"singctl/internal/license"
)

func TestFileStore_RoundTripAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "licenses.json")
	s, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := Record{
		Claims:    license.Claims{ID: "id1", Subject: "a"},
		Token:     "tok",
		Status:    StatusActive,
		CreatedAt: 100,
	}
	if err := s.Put(rec); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus("id1", StatusRevoked); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("missing"); err != ErrNotFound {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}

	// Reopen from disk: state persisted.
	s2, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Get("id1")
	if err != nil || got.Status != StatusRevoked || got.Claims.Subject != "a" {
		t.Errorf("reloaded record wrong: %+v err=%v", got, err)
	}
	if list, _ := s2.List(); len(list) != 1 {
		t.Errorf("List len = %d, want 1", len(list))
	}
}
