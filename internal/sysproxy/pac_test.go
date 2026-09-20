package sysproxy

import (
	"os"
	"testing"
)

// includeGoldenDomains/excludeGoldenRUDomains are the fixed inputs the golden
// files under testdata/ were generated from — see the (deleted) one-off
// generator this test's comment documents; regenerate by writing the PAC
// output to the golden path if this fixture ever changes intentionally.
var (
	includeGoldenDomains   = []string{"google.com", "openai.com", "anthropic.com"}
	excludeGoldenRUDomains = []string{"2gis.com", "3ebra.net", "yandex.net"}
	goldenProxyDirective   = "PROXY 127.0.0.1:2080"
)

func readGolden(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	return string(data)
}

// TestGenerateIncludePAC_Golden pins GenerateIncludePAC's exact byte output
// for a plain domain-only proxyRules list — unchanged from before the INI
// rework (no globs/CIDRs involved), so this golden did not need to change.
func TestGenerateIncludePAC_Golden(t *testing.T) {
	got := GenerateIncludePAC(includeGoldenDomains, goldenProxyDirective)
	want := readGolden(t, "testdata/include.pac")
	if got != want {
		t.Errorf("GenerateIncludePAC mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestGeneratePAC_DefaultConfig_Include is the task's "DEFAULT config"
// no-regression proof for include mode: DefaultConfig() (BypassPlainHostnames
// true, empty Direct/Proxy) with Proxy set to the golden fixture, run through
// the Config-driven GeneratePAC entrypoint, must still equal testdata's
// golden — proving the new rule-classification machinery produces identical
// output to the old hardcoded include-mode generator for a pure-domain list.
func TestGeneratePAC_DefaultConfig_Include(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeInclude
	cfg.Proxy = includeGoldenDomains
	got, err := GeneratePAC(cfg)
	if err != nil {
		t.Fatalf("GeneratePAC: %v", err)
	}
	want := readGolden(t, "testdata/include.pac")
	if got != want {
		t.Errorf("GeneratePAC(default include config) mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestGeneratePAC_DefaultConfig_Exclude is the exclude-mode counterpart:
// DefaultConfig() (which now seeds Direct with DefaultDirectRules — the
// RFC1918/CGNAT/link-local/loopback CIDRs and the *.ExampleOrganization.*/*.ru/*.xn--p1ai/
// *.local wildcards that used to be hardcoded in GenerateExcludePAC) plus the
// golden RU-domains fixture appended, must equal testdata/exclude.pac.
//
// NOTE on the one intentional golden diff: sections 3 and 4's comments
// changed from the old company-specific wording ("Corporate ExampleOrganization on any TLD,
// and Russian TLDs...", "Russian services on non-.ru TLDs...") to generic
// wording ("Wildcard [direct] rules...", "Domain [direct] rules..."), because
// those rules are now user-configurable DATA (Config.Direct), not hardcoded
// ExampleOrganization/ru literals — the old wording would be actively misleading for a custom
// [direct] list that has nothing to do with ExampleOrganization or Russia. Sections 1 and 2
// (bare-hostname rule, CIDR block incl. its exact column alignment) are
// untouched. See TestMatchesExcludeDirect_DomainRuleSemantics /
// TestMatchesExcludeDirect_DefaultRules_MatchOriginalHardcoding below for the
// behavioural (not just textual) no-regression proof.
func TestGeneratePAC_DefaultConfig_Exclude(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeExclude
	cfg.Host, cfg.Port = "127.0.0.1", 2080
	cfg.Direct = append(append([]string(nil), DefaultDirectRules...), excludeGoldenRUDomains...)
	got, err := GeneratePAC(cfg)
	if err != nil {
		t.Fatalf("GeneratePAC: %v", err)
	}
	want := readGolden(t, "testdata/exclude.pac")
	if got != want {
		t.Errorf("GeneratePAC(default exclude config) mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestGeneratePAC_Dispatch(t *testing.T) {
	cfg := Config{Mode: ModeInclude, Host: "127.0.0.1", Port: 2080, Proxy: includeGoldenDomains}
	got, err := GeneratePAC(cfg)
	if err != nil {
		t.Fatalf("GeneratePAC: %v", err)
	}
	if want := GenerateIncludePAC(includeGoldenDomains, cfg.ProxyDirective()); got != want {
		t.Errorf("GeneratePAC(include) = %q, want %q", got, want)
	}

	cfg = Config{Mode: ModeExclude, Host: "127.0.0.1", Port: 2080, Direct: excludeGoldenRUDomains, BypassPlainHostnames: true}
	got, err = GeneratePAC(cfg)
	if err != nil {
		t.Fatalf("GeneratePAC: %v", err)
	}
	if want := GenerateExcludePAC(nil, excludeGoldenRUDomains, cfg.ProxyDirective(), true); got != want {
		t.Errorf("GeneratePAC(exclude) = %q, want %q", got, want)
	}

	if _, err := GeneratePAC(Config{Mode: ModeOff}); err == nil {
		t.Error("GeneratePAC(off) should error — off has no PAC")
	}
}

// TestGenerateExcludePAC_ProxyOverride pins the new [proxy]-wins-over-
// [direct] behaviour end to end: a host matching BOTH proxyRules and
// directRules must render as PROXY, and the override section must be emitted
// BEFORE (and thus win over) every DIRECT rule, including the bare-hostname
// one.
func TestGenerateExcludePAC_ProxyOverride(t *testing.T) {
	cfg := Config{
		Host: "127.0.0.1", Port: 2080,
		Proxy:                []string{"openai.com"},
		Direct:               []string{"openai.com", "10.0.0.0/8"},
		BypassPlainHostnames: true,
	}
	if MatchesExcludeDirect(cfg, "openai.com") {
		t.Error("openai.com matches Proxy too — should NOT be DIRECT")
	}
	if MatchesExcludeDirect(cfg, "api.openai.com") {
		t.Error("api.openai.com (subdomain of a Proxy entry) should NOT be DIRECT")
	}
	// A host that ISN'T overridden still gets the normal DIRECT treatment.
	if !MatchesExcludeDirect(cfg, "10.1.2.3") {
		t.Error("10.1.2.3 (only in Direct) should be DIRECT")
	}

	pac := GenerateExcludePAC(cfg.Proxy, cfg.Direct, cfg.ProxyDirective(), cfg.BypassPlainHostnames)
	if idx := indexOf(pac, "// 0) Explicit [proxy] override"); idx < 0 {
		t.Fatalf("generated PAC missing the proxy-override section:\n%s", pac)
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestGenerateExcludePAC_BypassPlainHostnamesToggle covers both states of
// the new bypass_plain_hostnames setting: the isPlainHostName line (and its
// DIRECT behaviour) appears only when true.
func TestGenerateExcludePAC_BypassPlainHostnamesToggle(t *testing.T) {
	cfgOn := Config{Host: "127.0.0.1", Port: 2080, BypassPlainHostnames: true}
	pacOn := GenerateExcludePAC(cfgOn.Proxy, cfgOn.Direct, cfgOn.ProxyDirective(), cfgOn.BypassPlainHostnames)
	if idx := indexOf(pacOn, "isPlainHostName(host)"); idx < 0 {
		t.Errorf("bypass_plain_hostnames=true: expected isPlainHostName(host) check in PAC:\n%s", pacOn)
	}
	if !MatchesExcludeDirect(cfgOn, "intranet") {
		t.Error("bypass_plain_hostnames=true: bare hostname should be DIRECT")
	}

	cfgOff := Config{Host: "127.0.0.1", Port: 2080, BypassPlainHostnames: false}
	pacOff := GenerateExcludePAC(cfgOff.Proxy, cfgOff.Direct, cfgOff.ProxyDirective(), cfgOff.BypassPlainHostnames)
	if idx := indexOf(pacOff, "isPlainHostName(host)"); idx >= 0 {
		t.Errorf("bypass_plain_hostnames=false: did not expect isPlainHostName(host) check in PAC:\n%s", pacOff)
	}
	if MatchesExcludeDirect(cfgOff, "intranet") {
		t.Error("bypass_plain_hostnames=false: bare hostname should NOT be DIRECT (falls through to PROXY)")
	}
}

// TestGenerateExcludePAC_CIDRvsDomainRouting pins that a CIDR entry only
// ever matches a literal IPv4 host (never a domain name, even one that looks
// numeric-ish) and a bare-domain entry only ever matches via the
// dotted-suffix walk (never treated as a CIDR/glob) — in BOTH modes.
func TestGenerateExcludePAC_CIDRvsDomainRouting(t *testing.T) {
	cfg := Config{
		Host: "127.0.0.1", Port: 2080,
		Direct: []string{"10.0.0.0/8", "example.com"},
	}
	tests := []struct {
		host string
		want bool
	}{
		{"10.1.2.3", true},        // CIDR literal match
		{"example.com", true},     // domain exact match
		{"api.example.com", true}, // domain subdomain match
		{"10.0.0.0.8", false},     // NOT a CIDR match (not a literal IPv4 host)
		{"8.8.8.8", false},        // public IP, no CIDR matches
		{"notexample.com", false}, // must be a dotted-suffix match, not substring
	}
	for _, tt := range tests {
		if got := MatchesExcludeDirect(cfg, tt.host); got != tt.want {
			t.Errorf("MatchesExcludeDirect(exclude, %q) = %v, want %v", tt.host, got, tt.want)
		}
	}

	include := Config{Host: "127.0.0.1", Port: 2080, Proxy: []string{"10.0.0.0/8", "example.com"}}
	for _, tt := range tests {
		if got := MatchesIncludeProxy(include, tt.host); got != tt.want {
			t.Errorf("MatchesIncludeProxy(include, %q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

// TestMatchesExcludeDirect_DomainRuleSemantics pins the exclude-mode decision
// table (packaging/macos/gen-exclude-pac.sh's original rule set, now driven
// by DefaultDirectRules as data) against representative hosts, one per rule,
// per the task's semantics checklist: a .ru host, a corporate host, an
// RFC1918 literal, a simple hostname, and an allowlisted (RU) domain.
func TestMatchesExcludeDirect_DomainRuleSemantics(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeExclude
	cfg.Direct = append(append([]string(nil), DefaultDirectRules...), "vk.com")

	tests := []struct {
		name string
		host string
		want bool // true = DIRECT
	}{
		{"simple hostname (no dot)", "printer", true},
		{"simple hostname (no dot), single label", "intranet", true},
		{"dotted .ru host", "yandex.ru", true},
		{"bare ru TLD", "ru", true},
		{"dotted .xn--p1ai (рф) host", "почта.xn--p1ai", true},
		{"corporate ExampleOrganization host", "scm.example.invalid", true},
		{"corporate ExampleOrganization host, non-ru TLD", "portal.example.invalid", true},
		// shExpMatch(host, "*.ExampleOrganization.*") requires a literal ".ExampleOrganization." substring, so a
		// bare "ExampleOrganization.<tld>" (no subdomain, hence no leading dot before "ExampleOrganization")
		// does NOT match rule 3 — a faithful quirk of the original PAC this
		// port preserves rather than "fixes". Use a non-.ru TLD so rule 3's
		// .ru wildcard doesn't independently make it DIRECT anyway.
		{"ExampleOrganization bare second-level, no leading dot before ExampleOrganization — NOT matched by *.ExampleOrganization.*", "example.invalid", false},
		{".local host", "myhost.local", true},
		{"RFC1918 10/8 literal", "10.1.2.3", true},
		{"RFC1918 172.16/12 literal", "172.16.5.5", true},
		{"RFC1918 192.168/16 literal", "192.168.1.1", true},
		{"CGNAT 100.64/10 literal", "100.64.0.5", true},
		{"link-local literal", "169.254.1.1", true},
		{"loopback literal", "127.0.0.1", true},
		{"public IP literal", "8.8.8.8", false},
		{"allowlisted RU domain, exact", "vk.com", true},
		{"allowlisted RU domain, subdomain", "api.vk.com", true},
		{"non-listed foreign host", "example.com", false},
		{"non-listed foreign host, dotted", "openai.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MatchesExcludeDirect(cfg, tt.host); got != tt.want {
				t.Errorf("MatchesExcludeDirect(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestMatchesIncludeProxy_DomainRuleSemantics(t *testing.T) {
	cfg := Config{Proxy: []string{"openai.com", "anthropic.com"}}
	tests := []struct {
		host string
		want bool
	}{
		{"openai.com", true},
		{"api.openai.com", true},
		{"chat.openai.com", true},
		{"anthropic.com", true},
		{"claude.anthropic.com", true},
		{"example.com", false},
		{"notopenai.com", false}, // must be a dotted-suffix match, not substring
	}
	for _, tt := range tests {
		if got := MatchesIncludeProxy(cfg, tt.host); got != tt.want {
			t.Errorf("MatchesIncludeProxy(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

// TestClassifyRules pins the entry-shape classification (domain vs glob vs
// CIDR) the whole rule-matching/PAC-rendering machinery is built on.
func TestClassifyRules(t *testing.T) {
	domains, globs, cidrs, nets := classifyRules([]string{
		"OpenAI.com", " anthropic.com ", "*.githubusercontent.com", "*.ExampleOrganization.*",
		"10.0.0.0/8", "not-a-cidr/oops", "::1/128", "", "openai.com", // dup, case-folded
	})
	if want := []string{"openai.com", "anthropic.com"}; !equalStrings(domains, want) {
		t.Errorf("domains = %v, want %v", domains, want)
	}
	if want := []string{"*.githubusercontent.com", "*.ExampleOrganization.*"}; !equalStrings(globs, want) {
		t.Errorf("globs = %v, want %v", globs, want)
	}
	if len(cidrs) != 1 || cidrs[0] != "10.0.0.0/8" || len(nets) != 1 {
		t.Errorf("cidrs/nets = %v/%v, want just 10.0.0.0/8 (bad and non-IPv4 CIDRs dropped)", cidrs, nets)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
