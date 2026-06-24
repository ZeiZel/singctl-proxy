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

func TestAppBaseAndSameApp(t *testing.T) {
	if got := appBase("Cursor Helper (Renderer)"); got != "cursor" {
		t.Errorf("appBase helper = %q, want cursor", got)
	}
	if got := appBase("Google Chrome Helper (GPU)"); got != "google chrome" {
		t.Errorf("appBase chrome = %q, want google chrome", got)
	}
	if !sameApp("Cursor", "Cursor Helper (Renderer)") {
		t.Error("Cursor and its helper should be same app")
	}
	if sameApp("Cursor", "Finder") {
		t.Error("Cursor and Finder are not the same app")
	}
	if sameApp("", "") {
		t.Error("empty names must never match")
	}
}

func TestParseStatPPID(t *testing.T) {
	// comm with spaces and parens must not break field 4 (ppid) extraction.
	cases := map[string]int{
		"4242 (curl) S 100 4242 100 0 -1 ...":                       100,
		"77 (Web Content (1)) R 55 77 55 ...":                       55,
		"9 ((odd)name) S 1 9 ...":                                   1,
		"garbage-without-paren":                                     0,
	}
	for in, want := range cases {
		if got := parseStatPPID(in); got != want {
			t.Errorf("parseStatPPID(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParsePSPpid(t *testing.T) {
	data := []byte(
		"  100      1 /sbin/launchd\n" +
			" 4242    100 /Applications/Cursor.app/Contents/MacOS/Cursor\n" +
			" 4243   4242 /Applications/Cursor.app/Contents/Frameworks/Cursor Helper (Renderer).app/Contents/MacOS/Cursor Helper (Renderer)\n")
	ppid, name := parsePSPpid(data)
	if ppid[4243] != 4242 || ppid[4242] != 100 {
		t.Fatalf("ppid map wrong: %+v", ppid)
	}
	if name[4242] != "Cursor" {
		t.Errorf("name[4242] = %q, want Cursor", name[4242])
	}
	if name[4243] != "Cursor Helper (Renderer)" {
		t.Errorf("name[4243] = %q", name[4243])
	}
}

func TestGroupApps_FoldsHelpersUnderMainApp(t *testing.T) {
	// Tree: launchd(1) -> Cursor(4242, no socket) -> 3 helpers (with sockets).
	ppid := map[int]int{4242: 1, 4243: 4242, 4244: 4242, 4245: 4243}
	name := map[int]string{
		1:    "launchd",
		4242: "Cursor",
		4243: "Cursor Helper (Renderer)",
		4244: "Cursor Helper (GPU)",
		4245: "Cursor Helper (Renderer)",
	}
	// Only helpers hold sockets; the main Cursor process has none.
	procs := []Process{
		{PID: 4243, Name: "Cursor Helper (Renderer)", Ports: []int{443}},
		{PID: 4244, Name: "Cursor Helper (GPU)", Ports: []int{8080}},
		{PID: 4245, Name: "Cursor Helper (Renderer)", Ports: []int{443}},
	}
	apps := groupApps(procs, ppid, name)
	if len(apps) != 1 {
		t.Fatalf("want 1 app, got %d: %+v", len(apps), apps)
	}
	a := apps[0]
	if a.PID != 4242 || a.Name != "Cursor" {
		t.Errorf("root = PID %d %q, want 4242 Cursor", a.PID, a.Name)
	}
	if a.Children != 3 {
		t.Errorf("children = %d, want 3", a.Children)
	}
	if a.PortsString() != ":443 :8080" {
		t.Errorf("ports = %q, want :443 :8080", a.PortsString())
	}
}

func TestGroupApps_StandaloneProcessIsOwnApp(t *testing.T) {
	// curl spawned from a shell: parent is a launcher, so it never climbs.
	ppid := map[int]int{500: 400, 400: 1}
	name := map[int]string{500: "curl", 400: "bash", 1: "init"}
	procs := []Process{{PID: 500, Name: "curl", Ports: []int{54321}}}
	apps := groupApps(procs, ppid, name)
	if len(apps) != 1 || apps[0].PID != 500 || apps[0].Children != 0 {
		t.Fatalf("standalone curl mis-grouped: %+v", apps)
	}
}

func TestGroupApps_UnrelatedSiblingsNotMerged(t *testing.T) {
	// Two unrelated apps under the same shell must stay separate.
	ppid := map[int]int{10: 1, 20: 1}
	name := map[int]string{10: "firefox", 20: "slack", 1: "systemd"}
	procs := []Process{
		{PID: 10, Name: "firefox", Ports: []int{443}},
		{PID: 20, Name: "slack", Ports: []int{443}},
	}
	apps := groupApps(procs, ppid, name)
	if len(apps) != 2 {
		t.Fatalf("want 2 apps, got %d: %+v", len(apps), apps)
	}
}
