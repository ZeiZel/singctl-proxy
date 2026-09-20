package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSysproxyConfig_Missing_ReturnsNilNotError(t *testing.T) {
	fs := newFakeFS()
	s := NewStore(fs, "/home/u", 1000, 1000)

	// F1b: a fresh install (nothing ever saved) must not fail the daemon's
	// startup restore — nil/nil means "nothing to restore," not an error.
	data, err := s.LoadSysproxyConfig()
	if err != nil || data != nil {
		t.Fatalf("fresh LoadSysproxyConfig = (%v,%v), want (nil,nil)", data, err)
	}
}

func TestSysproxyConfig_RoundTrip_AndChowns(t *testing.T) {
	fs := newFakeFS()
	s := NewStore(fs, "/Users/mikhail", 501, 20)

	ini := []byte("[settings]\nmode = exclude\nhost = 127.0.0.1\nport = 2080\nservice = Wi-Fi\npac_port = 0\nbypass_plain_hostnames = true\n")
	if err := s.SaveSysproxyConfig(ini); err != nil {
		t.Fatalf("SaveSysproxyConfig: %v", err)
	}

	wantPath := filepath.Join("/Users/mikhail", ".config", "singctl", "sysproxy.ini")
	got, ok := fs.files[wantPath]
	if !ok {
		t.Fatalf("sysproxy config not written to %s (files: %v)", wantPath, fs.files)
	}
	if string(got) != string(ini) {
		t.Errorf("stored bytes = %q, want %q", got, ini)
	}

	var fileChowned bool
	for _, c := range fs.chowns {
		if c.name == wantPath {
			if c.uid != 501 || c.gid != 20 {
				t.Errorf("chown to %d:%d, want 501:20", c.uid, c.gid)
			}
			fileChowned = true
		}
	}
	if !fileChowned {
		t.Error("sysproxy config file was not chowned back to the real user")
	}

	loaded, err := s.LoadSysproxyConfig()
	if err != nil {
		t.Fatalf("LoadSysproxyConfig: %v", err)
	}
	if string(loaded) != string(ini) {
		t.Errorf("LoadSysproxyConfig = %q, want %q", loaded, ini)
	}
}

func TestSysproxyConfig_ReadError_Propagates(t *testing.T) {
	fs := &erroringReadFS{fakeFS: newFakeFS()}
	s := NewStore(fs, "/home/u", 1000, 1000)

	if _, err := s.LoadSysproxyConfig(); err == nil {
		t.Fatal("LoadSysproxyConfig with a failing FS.ReadFile should return an error")
	}
}

// erroringReadFS wraps fakeFS but fails every ReadFile with something other
// than os.ErrNotExist — simulating a store read error (permissions, a
// corrupt filesystem, ...) distinct from "the file was never created."
type erroringReadFS struct{ *fakeFS }

func (f *erroringReadFS) ReadFile(string) ([]byte, error) {
	return nil, os.ErrPermission
}
