package profile

import (
	"path/filepath"
	"reflect"
	"testing"

	"singctl/internal/firewall"
)

func TestStore_FirewallRules_RoundTrip(t *testing.T) {
	fs := newFakeFS()
	s := NewStore(fs, "/Users/mikhail", 501, 20)

	want := []firewall.Rule{
		{ID: "1", Action: firewall.ActionBlock, Domain: "ads.example.com"},
		{ID: "2", Action: firewall.ActionAllow, Process: "codex"},
	}
	if err := s.SaveFirewallRules(want); err != nil {
		t.Fatalf("SaveFirewallRules: %v", err)
	}

	wantPath := filepath.Join("/Users/mikhail", ".config", "singctl", "firewall.json")
	if _, ok := fs.files[wantPath]; !ok {
		t.Fatalf("firewall rules not written to %s (files: %v)", wantPath, fs.files)
	}

	got, err := s.LoadFirewallRules()
	if err != nil {
		t.Fatalf("LoadFirewallRules: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LoadFirewallRules() = %+v, want %+v", got, want)
	}

	var chowned bool
	for _, c := range fs.chowns {
		if c.name == wantPath && c.uid == 501 && c.gid == 20 {
			chowned = true
		}
	}
	if !chowned {
		t.Errorf("firewall.json was not chowned back to the real user: %+v", fs.chowns)
	}
}

func TestStore_LoadFirewallRules_MissingFileIsEmptyNotError(t *testing.T) {
	fs := newFakeFS()
	s := NewStore(fs, "/Users/mikhail", 501, 20)

	got, err := s.LoadFirewallRules()
	if err != nil {
		t.Fatalf("LoadFirewallRules: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("LoadFirewallRules() = %+v, want empty", got)
	}
}

func TestStore_LoadFirewallRules_CorruptFileErrors(t *testing.T) {
	fs := newFakeFS()
	s := NewStore(fs, "/Users/mikhail", 501, 20)
	path := filepath.Join("/Users/mikhail", ".config", "singctl", "firewall.json")
	fs.files[path] = []byte("not json")

	if _, err := s.LoadFirewallRules(); err == nil {
		t.Fatal("expected error for corrupt firewall.json")
	}
}

func TestStore_SaveFirewallRules_EmptyRemovesFile(t *testing.T) {
	fs := newFakeFS()
	s := NewStore(fs, "/Users/mikhail", 501, 20)
	if err := s.SaveFirewallRules([]firewall.Rule{{ID: "1", Action: firewall.ActionBlock, Domain: "x.com"}}); err != nil {
		t.Fatalf("SaveFirewallRules: %v", err)
	}
	if err := s.SaveFirewallRules(nil); err != nil {
		t.Fatalf("SaveFirewallRules(nil): %v", err)
	}
	path := filepath.Join("/Users/mikhail", ".config", "singctl", "firewall.json")
	if _, ok := fs.files[path]; ok {
		t.Errorf("firewall.json still present after saving an empty rule set")
	}
}
