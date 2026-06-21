package proclist

import "testing"

func TestLocalPortFromName(t *testing.T) {
	cases := map[string]int{
		"127.0.0.1:54321->1.2.3.4:443": 54321,
		"*:8080":                       8080,
		"127.0.0.1:8080 (LISTEN)":      8080,
		"[::1]:631":                    631,
		"garbage":                      0,
	}
	for in, want := range cases {
		if got := localPortFromName(in); got != want {
			t.Errorf("localPortFromName(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseLsofFields(t *testing.T) {
	// Two processes; curl has one connected socket, nginx two listeners.
	data := []byte("p4242\n" +
		"ccurl\n" +
		"n127.0.0.1:54321->1.2.3.4:443\n" +
		"p77\n" +
		"cnginx\n" +
		"n*:80\n" +
		"n*:443\n" +
		"p77\n" + // same pid again (lsof can repeat) — must merge
		"cnginx\n" +
		"n*:443\n")
	got := parseLsofFields(data)
	if len(got) != 2 {
		t.Fatalf("want 2 processes, got %d: %+v", len(got), got)
	}
	// sorted by name: curl, nginx
	if got[0].Name != "curl" || got[0].PID != 4242 || len(got[0].Ports) != 1 || got[0].Ports[0] != 54321 {
		t.Errorf("curl parsed wrong: %+v", got[0])
	}
	if got[1].Name != "nginx" || got[1].PID != 77 {
		t.Errorf("nginx parsed wrong: %+v", got[1])
	}
	if len(got[1].Ports) != 2 || got[1].Ports[0] != 80 || got[1].Ports[1] != 443 {
		t.Errorf("nginx ports = %v, want [80 443] deduped+sorted", got[1].Ports)
	}
}

func TestPortsString(t *testing.T) {
	if got := (Process{Ports: []int{80, 443}}).PortsString(); got != ":80 :443" {
		t.Errorf("PortsString = %q", got)
	}
	if got := (Process{}).PortsString(); got != "" {
		t.Errorf("empty PortsString = %q, want empty", got)
	}
}

func TestParseProcNetPorts(t *testing.T) {
	// header + one listener (0x1F90=8080, inode 12345) + one connection
	// (0xD431=54321, inode 67890).
	data := []byte("  sl  local_address rem_address   st ... inode\n" +
		"   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000 0 12345 1 x\n" +
		"   1: 0100007F:D431 01020304:01BB 01 00000000:00000000 00:00000000 00000000  1000 0 67890 1 x\n")
	m := parseProcNetPorts(data)
	if m[12345] != 8080 {
		t.Errorf("inode 12345 port = %d, want 8080", m[12345])
	}
	if m[67890] != 54321 {
		t.Errorf("inode 67890 port = %d, want 54321", m[67890])
	}
}

func TestNewLister_NonNil(t *testing.T) {
	if NewLister() == nil {
		t.Fatal("NewLister returned nil")
	}
}
