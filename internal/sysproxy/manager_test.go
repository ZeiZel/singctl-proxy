package sysproxy

import (
	"encoding/json"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// fakeNetworkSetup is an in-memory NetworkSetup for Manager tests: no
// `networksetup` binary, no macOS, no root. calls records every method
// invocation in order, so tests can assert exactly what Manager did (and, as
// importantly, did NOT do).
type fakeNetworkSetup struct {
	mu    sync.Mutex
	calls []string

	autoURL       map[string]string
	autoEnabled   map[string]bool
	webEnabled    map[string]bool
	bypassDomains map[string][]string
}

func newFakeNetworkSetup() *fakeNetworkSetup {
	return &fakeNetworkSetup{
		autoURL:       map[string]string{},
		autoEnabled:   map[string]bool{},
		webEnabled:    map[string]bool{},
		bypassDomains: map[string][]string{},
	}
}

func (f *fakeNetworkSetup) record(call string) {
	f.calls = append(f.calls, call)
}

func (f *fakeNetworkSetup) SetAutoProxyURL(service, url string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("SetAutoProxyURL " + service + " " + url)
	f.autoURL[service] = url
	f.autoEnabled[service] = true
	return nil
}

func (f *fakeNetworkSetup) DisableAutoProxy(service string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("DisableAutoProxy " + service)
	f.autoEnabled[service] = false
	return nil
}

func (f *fakeNetworkSetup) DisableWebProxy(service string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("DisableWebProxy " + service)
	f.webEnabled[service] = false
	return nil
}

func (f *fakeNetworkSetup) SetBypassDomains(service string, domains []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("SetBypassDomains " + service + " " + strings.Join(domains, ","))
	if len(domains) == 0 {
		delete(f.bypassDomains, service)
	} else {
		f.bypassDomains[service] = append([]string(nil), domains...)
	}
	return nil
}

func (f *fakeNetworkSetup) Status(service string) (State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return State{
		AutoProxyEnabled: f.autoEnabled[service],
		AutoProxyURL:     f.autoURL[service],
		WebProxyEnabled:  f.webEnabled[service],
		BypassDomains:    append([]string(nil), f.bypassDomains[service]...),
	}, nil
}

func (f *fakeNetworkSetup) Services() ([]string, error) {
	return []string{"Wi-Fi", "Ethernet"}, nil
}

func (f *fakeNetworkSetup) callLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func testIncludeConfig() Config {
	return Config{
		Mode: ModeInclude, Host: "127.0.0.1", Port: 2080,
		Proxy:   []string{"google.com", "openai.com"},
		Service: "Wi-Fi", PACPort: 0, // 0: let the test bind an ephemeral port
	}
}

func testExcludeConfig() Config {
	return Config{
		Mode: ModeExclude, Host: "127.0.0.1", Port: 2080,
		Direct:  []string{"vk.com"},
		Service: "Wi-Fi", PACPort: 0,
		BypassPlainHostnames: true,
	}
}

func TestManager_Apply_Off_DisablesAutoAndWebProxy(t *testing.T) {
	ns := newFakeNetworkSetup()
	m := NewManager(ns)

	// Go through include mode first so there is something to turn off, then
	// verify ModeOff disables BOTH mechanisms (the task's explicit test
	// requirement).
	if err := m.Apply(testIncludeConfig()); err != nil {
		t.Fatalf("Apply(include): %v", err)
	}
	defer m.Close()

	if err := m.Apply(Config{Mode: ModeOff, Service: "Wi-Fi"}); err != nil {
		t.Fatalf("Apply(off): %v", err)
	}

	calls := ns.callLog()
	var sawDisableAuto, sawDisableWeb bool
	for _, c := range calls {
		if strings.HasPrefix(c, "DisableAutoProxy Wi-Fi") {
			sawDisableAuto = true
		}
		if strings.HasPrefix(c, "DisableWebProxy Wi-Fi") {
			sawDisableWeb = true
		}
	}
	if !sawDisableAuto {
		t.Errorf("Apply(off) did not call DisableAutoProxy; calls = %v", calls)
	}
	if !sawDisableWeb {
		t.Errorf("Apply(off) did not call DisableWebProxy; calls = %v", calls)
	}

	st, _ := ns.Status("Wi-Fi")
	if st.AutoProxyEnabled {
		t.Error("after Apply(off), auto proxy still enabled")
	}
}

func TestManager_Apply_InvalidConfig_AppliesNothing(t *testing.T) {
	ns := newFakeNetworkSetup()
	m := NewManager(ns)

	bad := Config{Mode: "bogus", Service: "Wi-Fi"}
	if err := m.Apply(bad); err == nil {
		t.Fatal("Apply(invalid) should error")
	}
	if calls := ns.callLog(); len(calls) != 0 {
		t.Errorf("Apply(invalid) touched NetworkSetup: %v", calls)
	}
	if got := m.Config(); got.Mode != ModeOff {
		t.Errorf("Apply(invalid) changed the live config to %+v, want unchanged ModeOff default", got)
	}

	// Same for a structurally-valid-but-incomplete include config (no domains).
	if err := m.Apply(Config{Mode: ModeInclude, Host: "127.0.0.1", Port: 2080, Service: "Wi-Fi", PACPort: 21080}); err == nil {
		t.Fatal("Apply(include, no domains) should error")
	}
	if calls := ns.callLog(); len(calls) != 0 {
		t.Errorf("Apply(include, no domains) touched NetworkSetup: %v", calls)
	}
}

func TestManager_Apply_Include_SetsAutoProxyURLToPACServer(t *testing.T) {
	ns := newFakeNetworkSetup()
	m := NewManager(ns)
	defer m.Close()

	if err := m.Apply(testIncludeConfig()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	st, _ := ns.Status("Wi-Fi")
	if !st.AutoProxyEnabled {
		t.Error("auto proxy not enabled after Apply(include)")
	}
	if !strings.HasSuffix(st.AutoProxyURL, "/proxy.pac") {
		t.Errorf("AutoProxyURL = %q, want it to end in /proxy.pac", st.AutoProxyURL)
	}
	if st.WebProxyEnabled {
		t.Error("manual web proxy should be disabled once a PAC is applied")
	}

	// The PAC server should actually be serving the generated PAC.
	status := m.Status()
	if status.PACURL != st.AutoProxyURL {
		t.Errorf("Status().PACURL = %q, want %q", status.PACURL, st.AutoProxyURL)
	}
	if !status.PACServerUp {
		t.Error("Status().PACServerUp = false, want true once Apply(include) has run")
	}
	if status.DomainCount != 2 {
		t.Errorf("Status().DomainCount = %d, want 2", status.DomainCount)
	}
}

func TestManager_Apply_Exclude_DomainCountUsesDirect(t *testing.T) {
	ns := newFakeNetworkSetup()
	m := NewManager(ns)
	defer m.Close()

	if err := m.Apply(testExcludeConfig()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := m.Status().DomainCount; got != 1 {
		t.Errorf("DomainCount = %d, want 1 (Direct)", got)
	}
}

// TestManager_Apply_SetsBypassDomains_FromDirect pins gap #1: Apply must
// pass the domain/glob-shaped Direct entries to NetworkSetup.SetBypassDomains
// — a CIDR entry (10.0.0.0/8 below) must NOT be forwarded, since macOS
// bypass lists take hosts/domains, not CIDRs.
func TestManager_Apply_SetsBypassDomains_FromDirect(t *testing.T) {
	ns := newFakeNetworkSetup()
	m := NewManager(ns)
	defer m.Close()

	cfg := testExcludeConfig()
	cfg.Direct = []string{"vk.com", "*.ru", "10.0.0.0/8"}
	if err := m.Apply(cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	st, _ := ns.Status("Wi-Fi")
	want := []string{"vk.com", "*.ru"}
	if !reflect.DeepEqual(st.BypassDomains, want) {
		t.Errorf("BypassDomains = %v, want %v (CIDR entry excluded)", st.BypassDomains, want)
	}
}

// TestManager_Apply_Off_ClearsBypassDomains pins the other half of gap #1:
// switching to ModeOff must clear the bypass-domain list, not just leave
// whatever a prior include/exclude Apply set.
func TestManager_Apply_Off_ClearsBypassDomains(t *testing.T) {
	ns := newFakeNetworkSetup()
	m := NewManager(ns)
	defer m.Close()

	cfg := testExcludeConfig()
	cfg.Direct = []string{"vk.com"}
	if err := m.Apply(cfg); err != nil {
		t.Fatalf("Apply(exclude): %v", err)
	}
	if st, _ := ns.Status("Wi-Fi"); len(st.BypassDomains) == 0 {
		t.Fatal("precondition failed: bypass domains not set after Apply(exclude)")
	}

	if err := m.Apply(Config{Mode: ModeOff, Service: "Wi-Fi"}); err != nil {
		t.Fatalf("Apply(off): %v", err)
	}
	st, _ := ns.Status("Wi-Fi")
	if len(st.BypassDomains) != 0 {
		t.Errorf("BypassDomains after Apply(off) = %v, want empty (cleared)", st.BypassDomains)
	}
}

func TestManager_Import_PlainDomainList_AppliesMergedConfig(t *testing.T) {
	ns := newFakeNetworkSetup()
	m := NewManager(ns)
	defer m.Close()

	// Seed a base include config so the import has something to merge onto.
	if err := m.Apply(testIncludeConfig()); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if err := m.Import([]byte("newdomain.com\n")); err != nil {
		t.Fatalf("Import: %v", err)
	}
	got := m.Config()
	if got.Mode != ModeInclude || got.Service != "Wi-Fi" {
		t.Errorf("Import(plain list) changed mode/service: %+v", got)
	}
	if want := []string{"newdomain.com"}; len(got.Proxy) != 1 || got.Proxy[0] != want[0] {
		t.Errorf("Import(plain list) Proxy = %v, want %v", got.Proxy, want)
	}
}

// TestManager_Import_INIConfig pins the primary import path: an INI payload
// (as SYSPROXY-IMPORT will typically carry) is decoded as a full config and
// applied.
func TestManager_Import_INIConfig(t *testing.T) {
	ns := newFakeNetworkSetup()
	m := NewManager(ns)
	defer m.Close()

	ini := []byte(`[settings]
mode = include
host = 127.0.0.1
port = 2080
service = Wi-Fi
pac_port = 0

[proxy]
openai.com
`)
	if err := m.Import(ini); err != nil {
		t.Fatalf("Import(ini): %v", err)
	}
	got := m.Config()
	if got.Mode != ModeInclude || got.Service != "Wi-Fi" {
		t.Errorf("Import(ini) = %+v", got)
	}
	if want := []string{"openai.com"}; !reflect.DeepEqual(got.Proxy, want) {
		t.Errorf("Import(ini) Proxy = %v, want %v", got.Proxy, want)
	}
}

func TestManager_Import_FullYAMLConfig(t *testing.T) {
	ns := newFakeNetworkSetup()
	m := NewManager(ns)
	defer m.Close()

	full := Config{Mode: ModeOff, Service: "Ethernet"}
	data, err := full.YAML()
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	if err := m.Import(data); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if got := m.Config(); got.Mode != ModeOff || got.Service != "Ethernet" {
		t.Errorf("Import(full yaml) = %+v, want %+v", got, full)
	}
}

func TestManager_ConfigYAML_RoundTripsThroughManager(t *testing.T) {
	ns := newFakeNetworkSetup()
	m := NewManager(ns)
	defer m.Close()

	if err := m.Apply(testExcludeConfig()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, err := m.ConfigYAML()
	if err != nil {
		t.Fatalf("ConfigYAML: %v", err)
	}
	got, err := ParseConfigYAML(data)
	if err != nil {
		t.Fatalf("ParseConfigYAML: %v", err)
	}
	if got.Mode != ModeExclude || len(got.Direct) != 1 {
		t.Errorf("round-tripped config = %+v", got)
	}
}

// TestManager_ConfigINI_RoundTripsThroughManager mirrors the YAML round-trip
// test above for the new primary format.
func TestManager_ConfigINI_RoundTripsThroughManager(t *testing.T) {
	ns := newFakeNetworkSetup()
	m := NewManager(ns)
	defer m.Close()

	if err := m.Apply(testExcludeConfig()); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data := m.ConfigINI()
	got, err := ParseINI(data)
	if err != nil {
		t.Fatalf("ParseINI: %v", err)
	}
	if got.Mode != ModeExclude || len(got.Direct) != 1 {
		t.Errorf("round-tripped config = %+v", got)
	}
}

// TestStatusJSONFieldNames pins the wire names of SYSPROXY-STATUS.
//
// The macOS app decodes this payload by exact key, and a mismatch does not fail
// loudly on the Go side at all — it surfaces only as a decoding error in the
// GUI. That is precisely what shipped once: the app expected camelCase
// ("pacServerUp"), the daemon sent snake_case, and the System proxy screen
// showed a decoder dump instead of its state. Renaming a field here means
// changing macos/Singctl/App/Core/Models.swift's CodingKeys in the same commit.
func TestStatusJSONFieldNames(t *testing.T) {
	data, err := json.Marshal(Status{Mode: ModeExclude, Service: "Wi-Fi", PACURL: "http://127.0.0.1:21080/x.pac", PACServerUp: true, DomainCount: 7})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mode", "service", "pac_url", "pac_server_up", "domain_count"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("SYSPROXY-STATUS lost the %q key; the macOS client decodes by exact name", key)
		}
	}

	// pac_url is omitempty: it is ABSENT while the proxy is off, so the client
	// must treat it as optional. Pin that too — making it non-optional on
	// either side breaks the common first-open case.
	off, _ := json.Marshal(Status{Mode: ModeOff, Service: "Wi-Fi"})
	var offDecoded map[string]any
	_ = json.Unmarshal(off, &offDecoded)
	if _, present := offDecoded["pac_url"]; present {
		t.Error("pac_url is expected to be omitted when off; the client relies on it being optional")
	}
}

// --- F1: PAC port conflict — see docs/v2-spec.md -----------------------

// fakePortDiagnostics is an in-memory PortDiagnostics for Manager tests — no
// lsof/ps/launchctl, no real process lookup, no real plist on disk.
type fakePortDiagnostics struct {
	holder    PortHolder
	holderOK  bool
	holderErr error

	present   bool
	owned     bool
	path      string
	legacyErr error

	removeCalled bool
	removeErr    error
}

func (f *fakePortDiagnostics) HolderOf(int) (PortHolder, bool, error) {
	return f.holder, f.holderOK, f.holderErr
}

func (f *fakePortDiagnostics) LegacyPACAgent() (bool, bool, string, error) {
	return f.present, f.owned, f.path, f.legacyErr
}

func (f *fakePortDiagnostics) RemoveLegacyPACAgent() error {
	f.removeCalled = true
	return f.removeErr
}

// TestDefaultConfig_PACPortIsEphemeral pins F1 item 1: the default is 0 ("OS
// picks a free port"), not the fixed 21080 the standalone mac-proxy utility
// also hardcodes — that shared fixed port is the whole root cause.
func TestDefaultConfig_PACPortIsEphemeral(t *testing.T) {
	if got := DefaultConfig().PACPort; got != 0 {
		t.Errorf("DefaultConfig().PACPort = %d, want 0 (OS-assigned by default)", got)
	}
}

// TestManager_Apply_DefaultPACPort_PublishesActualBoundPort exercises
// DefaultConfig()'s PACPort=0 end to end: Apply must bind an ephemeral port
// and the URL it hands to NetworkSetup.SetAutoProxyURL — and that
// Status().PACURL reports — must be the port actually bound, not 0 or the
// legacy 21080.
func TestManager_Apply_DefaultPACPort_PublishesActualBoundPort(t *testing.T) {
	ns := newFakeNetworkSetup()
	m := NewManager(ns)
	defer m.Close()

	cfg := DefaultConfig()
	cfg.Mode, cfg.Host, cfg.Port, cfg.Service = ModeInclude, "127.0.0.1", 2080, "Wi-Fi"
	cfg.Proxy = []string{"openai.com"}
	if err := m.Apply(cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	status := m.Status()
	if status.PACURL == "" {
		t.Fatal("Status().PACURL is empty after Apply with the default (ephemeral) pac_port")
	}
	if strings.Contains(status.PACURL, ":0/") || strings.Contains(status.PACURL, ":21080/") {
		t.Errorf("Status().PACURL = %q, want the actually-bound ephemeral port, not 0 or the legacy fixed port", status.PACURL)
	}
	st, _ := ns.Status("Wi-Fi")
	if st.AutoProxyURL != status.PACURL {
		t.Errorf("networksetup was told AutoProxyURL=%q, want it to match the actually-bound %q", st.AutoProxyURL, status.PACURL)
	}
}

// TestManager_Apply_PinnedPortOccupied_ErrorNamesHolder pins F1 item 3: a
// pinned pac_port that's genuinely already bound must produce an error
// naming the holder's pid and path. The bind conflict itself is real (a
// second listener on the same loopback port, not faked) — only identifying
// the holder is faked, since a hermetic test can't rely on lsof/ps behaving
// any particular way in CI.
func TestManager_Apply_PinnedPortOccupied_ErrorNamesHolder(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer ln.Close()
	occupied := ln.Addr().(*net.TCPAddr).Port

	ns := newFakeNetworkSetup()
	m := NewManager(ns).WithPortDiagnostics(&fakePortDiagnostics{
		holder: PortHolder{PID: 4242, Path: "/usr/bin/python3"}, holderOK: true,
	})
	defer m.Close()

	cfg := testIncludeConfig()
	cfg.PACPort = occupied
	err = m.Apply(cfg)
	if err == nil {
		t.Fatal("Apply with an occupied pinned pac_port should error")
	}
	if !strings.Contains(err.Error(), "4242") || !strings.Contains(err.Error(), "/usr/bin/python3") {
		t.Errorf("Apply error = %q, want it to name the holder's pid (4242) and path (/usr/bin/python3)", err.Error())
	}
}

// TestManager_Apply_PinnedPortOccupiedByLegacyAgent_ErrorNamesRemoval pins
// the rest of F1 item 3: when the holder is verifiably singctl's own legacy
// PAC LaunchAgent, the error must say so explicitly and state the exact
// command that removes it (or point at SYSPROXY-RECLAIM-PORT).
func TestManager_Apply_PinnedPortOccupiedByLegacyAgent_ErrorNamesRemoval(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer ln.Close()
	occupied := ln.Addr().(*net.TCPAddr).Port

	const plistPath = "/Users/tester/Library/LaunchAgents/com.singctl.pacserver.plist"
	ns := newFakeNetworkSetup()
	m := NewManager(ns).WithPortDiagnostics(&fakePortDiagnostics{
		holder: PortHolder{PID: 111, Path: "/usr/bin/python3"}, holderOK: true,
		present: true, owned: true, path: plistPath,
	})
	defer m.Close()

	cfg := testIncludeConfig()
	cfg.PACPort = occupied
	err = m.Apply(cfg)
	if err == nil {
		t.Fatal("Apply with an occupied pinned pac_port should error")
	}
	if !strings.Contains(err.Error(), plistPath) {
		t.Errorf("Apply error = %q, want it to name the legacy plist path", err.Error())
	}
	if !strings.Contains(err.Error(), "launchctl bootout") && !strings.Contains(err.Error(), "SYSPROXY-RECLAIM-PORT") {
		t.Errorf("Apply error = %q, want it to state the exact removal command or point at SYSPROXY-RECLAIM-PORT", err.Error())
	}
}

// TestManager_Apply_PinnedPortOccupiedByForeignProcess_NeverSuggestsRemoval
// pins the flip side: when the holder is NOT singctl's legacy agent, the
// error names the holder but must never suggest removing anything.
func TestManager_Apply_PinnedPortOccupiedByForeignProcess_NeverSuggestsRemoval(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer ln.Close()
	occupied := ln.Addr().(*net.TCPAddr).Port

	ns := newFakeNetworkSetup()
	m := NewManager(ns).WithPortDiagnostics(&fakePortDiagnostics{
		holder: PortHolder{PID: 555, Path: "/Applications/Slack.app/Contents/MacOS/Slack"}, holderOK: true,
		present: false,
	})
	defer m.Close()

	cfg := testIncludeConfig()
	cfg.PACPort = occupied
	err = m.Apply(cfg)
	if err == nil {
		t.Fatal("Apply with an occupied pinned pac_port should error")
	}
	if strings.Contains(err.Error(), "launchctl bootout") || strings.Contains(err.Error(), "SYSPROXY-RECLAIM-PORT") {
		t.Errorf("Apply error = %q, must not suggest a removal command for a foreign process", err.Error())
	}
}

// TestIsLegacyPACAgentPlist pins the ownership check reused from
// packaging/macos/scripts/preinstall: both "http.server" AND
// ".config/singctl" must be present, mirroring that script's
// `grep -q "http.server" ... && grep -q "\.config/singctl" ...`.
func TestIsLegacyPACAgentPlist(t *testing.T) {
	ours := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.singctl.pacserver</string>
	<key>ProgramArguments</key>
	<array>
		<string>/usr/bin/python3</string>
		<string>-m</string>
		<string>http.server</string>
		<string>21080</string>
		<string>--bind</string>
		<string>127.0.0.1</string>
		<string>--directory</string>
		<string>/Users/tester/.config/singctl</string>
	</array>
</dict>
</plist>`)
	if !IsLegacyPACAgentPlist(ours) {
		t.Error("IsLegacyPACAgentPlist should accept singctl's own legacy plist shape")
	}

	foreignHTTPServer := []byte(`<key>ProgramArguments</key><array>
	<string>/usr/bin/python3</string><string>-m</string><string>http.server</string>
	<string>8000</string><string>--directory</string><string>/Users/tester/Sites</string>
</array>`)
	if IsLegacyPACAgentPlist(foreignHTTPServer) {
		t.Error("IsLegacyPACAgentPlist should reject an http.server LaunchAgent that doesn't serve out of .config/singctl")
	}

	unrelated := []byte(`<key>Label</key><string>com.apple.something</string>`)
	if IsLegacyPACAgentPlist(unrelated) {
		t.Error("IsLegacyPACAgentPlist should reject an unrelated plist")
	}
}

// TestManager_ReclaimPort_NoDiagnostics pins F1 item 4's safety: with no
// PortDiagnostics attached at all, ReclaimPort must refuse rather than
// silently no-op or panic.
func TestManager_ReclaimPort_NoDiagnostics(t *testing.T) {
	m := NewManager(newFakeNetworkSetup())
	defer m.Close()
	if err := m.ReclaimPort(); err == nil {
		t.Fatal("ReclaimPort with no PortDiagnostics should error, not silently succeed")
	}
}

// TestManager_ReclaimPort_NothingPresent pins the "nothing to remove" case.
func TestManager_ReclaimPort_NothingPresent(t *testing.T) {
	diag := &fakePortDiagnostics{present: false}
	m := NewManager(newFakeNetworkSetup()).WithPortDiagnostics(diag)
	defer m.Close()
	if err := m.ReclaimPort(); err == nil {
		t.Fatal("ReclaimPort should error when no legacy PAC LaunchAgent exists")
	}
	if diag.removeCalled {
		t.Error("ReclaimPort must not call RemoveLegacyPACAgent when nothing is present")
	}
}

// TestManager_ReclaimPort_DeclinesForeignAgent pins the core safety
// requirement: RECLAIM-PORT must decline when the ownership check fails,
// and must NEVER call RemoveLegacyPACAgent in that case.
func TestManager_ReclaimPort_DeclinesForeignAgent(t *testing.T) {
	diag := &fakePortDiagnostics{present: true, owned: false, path: "/Users/tester/Library/LaunchAgents/com.singctl.pacserver.plist"}
	m := NewManager(newFakeNetworkSetup()).WithPortDiagnostics(diag)
	defer m.Close()
	if err := m.ReclaimPort(); err == nil {
		t.Fatal("ReclaimPort should decline a plist that fails the ownership check")
	}
	if diag.removeCalled {
		t.Error("ReclaimPort must never call RemoveLegacyPACAgent when ownership doesn't verify")
	}
}

// TestManager_ReclaimPort_SucceedsForOwnAgent pins the success path: when
// present AND owned, ReclaimPort performs the removal.
func TestManager_ReclaimPort_SucceedsForOwnAgent(t *testing.T) {
	diag := &fakePortDiagnostics{present: true, owned: true, path: "/Users/tester/Library/LaunchAgents/com.singctl.pacserver.plist"}
	m := NewManager(newFakeNetworkSetup()).WithPortDiagnostics(diag)
	defer m.Close()
	if err := m.ReclaimPort(); err != nil {
		t.Fatalf("ReclaimPort: %v", err)
	}
	if !diag.removeCalled {
		t.Error("ReclaimPort should have called RemoveLegacyPACAgent for a verifiably-owned legacy agent")
	}
}

// --- Restore (F1b in docs/v2-spec.md) ---

// TestManager_Restore_Off_TouchesNoNetworkSetup pins F1b item 4: a persisted
// mode = off config restores nothing and must not call ANY NetworkSetup
// method — not even the idempotent Disable* calls Apply's own ModeOff branch
// makes for an explicit user "turn it off" action.
func TestManager_Restore_Off_TouchesNoNetworkSetup(t *testing.T) {
	ns := newFakeNetworkSetup()
	m := NewManager(ns)
	defer m.Close()

	off := Config{Mode: ModeOff, Service: "Wi-Fi"}
	if err := m.Restore(off); err != nil {
		t.Fatalf("Restore(off): %v", err)
	}
	if calls := ns.callLog(); len(calls) != 0 {
		t.Errorf("Restore(off) touched NetworkSetup: %v", calls)
	}
	if got := m.Config(); got.Mode != ModeOff {
		t.Errorf("Restore(off) left Config().Mode = %q, want %q", got.Mode, ModeOff)
	}
	if url := m.Status().PACURL; url != "" {
		t.Errorf("Restore(off) should never start the PAC server; PACURL = %q", url)
	}
}

// TestManager_Restore_NonOff_RebindsFreshPort simulates a daemon restart: the
// original Manager (m1, standing in for the process that exited) applied an
// include config with pac_port = 0 and bound some ephemeral port; the
// persisted Config (what would have been written to disk, via INI round
// trip) still has pac_port = 0, because Config.PACPort is never rewritten
// with the actually-bound port. A fresh Manager (m2, standing in for the
// restarted daemon) restoring that same Config must bind its OWN fresh
// ephemeral port and re-apply `networksetup` with THAT port's URL — not the
// one m1 was using. This is the whole point of F1b item 2.
func TestManager_Restore_NonOff_RebindsFreshPort(t *testing.T) {
	ns1 := newFakeNetworkSetup()
	m1 := NewManager(ns1)
	defer m1.Close() // kept alive so its port stays reserved — proves m2 can't reuse it

	if err := m1.Apply(testIncludeConfig()); err != nil {
		t.Fatalf("Apply on the pre-restart manager: %v", err)
	}
	originalURL := m1.Status().PACURL
	if originalURL == "" {
		t.Fatal("pre-restart manager never bound a PAC URL")
	}

	// Round-trip exactly what gets persisted: the applied Config, rendered to
	// INI (Config.INI, the on-disk format) and parsed back (ParseINI) — same
	// as profile.Store.SaveSysproxyConfig/LoadSysproxyConfig would do.
	persisted := m1.Config()
	if persisted.PACPort != 0 {
		t.Fatalf("test setup: expected the persisted PACPort to stay 0, got %d", persisted.PACPort)
	}
	parsed, err := ParseINI(persisted.INI())
	if err != nil {
		t.Fatalf("ParseINI(persisted.INI()): %v", err)
	}

	ns2 := newFakeNetworkSetup()
	m2 := NewManager(ns2)
	defer m2.Close()

	if err := m2.Restore(parsed); err != nil {
		t.Fatalf("Restore on the post-restart manager: %v", err)
	}
	newURL := m2.Status().PACURL
	if newURL == "" {
		t.Fatal("Restore never bound a PAC URL")
	}
	if newURL == originalURL {
		t.Fatalf("Restore reused the pre-restart URL %q — it must bind a fresh port", originalURL)
	}

	st, _ := ns2.Status("Wi-Fi")
	if st.AutoProxyURL != newURL {
		t.Errorf("networksetup AutoProxyURL = %q, want the newly-bound %q", st.AutoProxyURL, newURL)
	}
	if !st.AutoProxyEnabled {
		t.Error("Restore should have enabled the automatic proxy on the network service")
	}

	var sawSetAutoProxyURL bool
	for _, c := range ns2.callLog() {
		if strings.HasPrefix(c, "SetAutoProxyURL Wi-Fi "+newURL) {
			sawSetAutoProxyURL = true
		}
	}
	if !sawSetAutoProxyURL {
		t.Errorf("Restore did not call SetAutoProxyURL with the newly-bound URL; calls = %v", ns2.callLog())
	}
}

// TestManager_Restore_InvalidConfig_AppliesNothing mirrors
// TestManager_Apply_InvalidConfig_AppliesNothing: a malformed persisted
// config (e.g. the store file was hand-edited into garbage) must leave the
// Manager untouched rather than half-adopting it — F1b item 3's "best
// effort" is the caller's (cmd/singctl/main.go) job of logging and
// continuing, not license for Restore itself to leave a broken half-state.
func TestManager_Restore_InvalidConfig_AppliesNothing(t *testing.T) {
	ns := newFakeNetworkSetup()
	m := NewManager(ns)
	defer m.Close()

	bad := Config{Mode: "bogus", Service: "Wi-Fi"}
	if err := m.Restore(bad); err == nil {
		t.Fatal("Restore(invalid) should error")
	}
	if calls := ns.callLog(); len(calls) != 0 {
		t.Errorf("Restore(invalid) touched NetworkSetup: %v", calls)
	}
	if got := m.Config(); got.Mode != ModeOff {
		t.Errorf("Restore(invalid) changed the live config to %+v, want unchanged ModeOff default", got)
	}
}
