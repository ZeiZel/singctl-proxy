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

func TestFileStore_BindResetDeviceRoundTrip(t *testing.T) {
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

	if err := s.BindDevice("missing", "dev1", "a@example.com"); err != ErrNotFound {
		t.Errorf("BindDevice(missing) = %v, want ErrNotFound", err)
	}

	if err := s.BindDevice("id1", "dev1", "a@example.com"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("id1")
	if err != nil {
		t.Fatal(err)
	}
	if got.DeviceID != "dev1" || got.Email != "a@example.com" || got.ActivatedAt == 0 {
		t.Errorf("after BindDevice: %+v", got)
	}

	// Last-wins rebind.
	if err := s.BindDevice("id1", "dev2", "b@example.com"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get("id1")
	if got.DeviceID != "dev2" || got.Email != "b@example.com" {
		t.Errorf("after rebind: %+v", got)
	}

	// Reopen from disk: binding persisted.
	s2, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := s2.Get("id1")
	if err != nil || got2.DeviceID != "dev2" || got2.Email != "b@example.com" {
		t.Errorf("reloaded binding wrong: %+v err=%v", got2, err)
	}

	if err := s2.ResetDevice("missing"); err != ErrNotFound {
		t.Errorf("ResetDevice(missing) = %v, want ErrNotFound", err)
	}
	if err := s2.ResetDevice("id1"); err != nil {
		t.Fatal(err)
	}
	got3, _ := s2.Get("id1")
	if got3.DeviceID != "" || got3.Email != "" || got3.ActivatedAt != 0 {
		t.Errorf("after ResetDevice: %+v", got3)
	}
}
