package sysproxy

import (
	"reflect"
	"testing"
)

func TestConfig_YAMLRoundTrip(t *testing.T) {
	cfg := Config{
		Mode: ModeExclude, Host: "127.0.0.1", Port: 2080,
		Direct: []string{"vk.com", "ok.ru"}, Service: "Wi-Fi", PACPort: 21080,
		BypassPlainHostnames: true,
	}
	data, err := cfg.YAML()
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	got, err := ParseConfigYAML(data)
	if err != nil {
		t.Fatalf("ParseConfigYAML: %v", err)
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Errorf("round trip mismatch:\n got  = %+v\n want = %+v\nyaml:\n%s", got, cfg, data)
	}
}

func TestConfig_YAMLRoundTrip_IncludeMode(t *testing.T) {
	cfg := Config{
		Mode: ModeInclude, Host: "127.0.0.1", Port: 2080,
		Proxy: []string{"google.com", "openai.com"}, Service: "Wi-Fi", PACPort: 21080,
	}
	data, err := cfg.YAML()
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	got, err := ParseConfigYAML(data)
	if err != nil {
		t.Fatalf("ParseConfigYAML: %v", err)
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Errorf("round trip mismatch:\n got  = %+v\n want = %+v\nyaml:\n%s", got, cfg, data)
	}
}

// TestConfig_YAML_LegacyAliasesAccepted pins backward-compatible YAML import:
// hand-written (or previously-persisted) YAML using the pre-INI domains/
// ru_domains keys still parses, folding onto Proxy/Direct.
func TestConfig_YAML_LegacyAliasesAccepted(t *testing.T) {
	data := []byte(`
mode: include
host: 127.0.0.1
port: 2080
service: Wi-Fi
pac_port: 21080
domains:
  - google.com
  - openai.com
`)
	got, err := ParseConfigYAML(data)
	if err != nil {
		t.Fatalf("ParseConfigYAML: %v", err)
	}
	if want := []string{"google.com", "openai.com"}; !reflect.DeepEqual(got.Proxy, want) {
		t.Errorf("legacy domains: Proxy = %v, want %v", got.Proxy, want)
	}

	data = []byte(`
mode: exclude
host: 127.0.0.1
port: 2080
service: Wi-Fi
pac_port: 21080
ru_domains:
  - vk.com
`)
	got, err = ParseConfigYAML(data)
	if err != nil {
		t.Fatalf("ParseConfigYAML: %v", err)
	}
	if want := []string{"vk.com"}; !reflect.DeepEqual(got.Direct, want) {
		t.Errorf("legacy ru_domains: Direct = %v, want %v", got.Direct, want)
	}
}

func TestParseDomainList(t *testing.T) {
	data := []byte(`# Domains routed THROUGH the Singctl proxy.
# blank lines and comments are ignored.

google.com
gstatic.com # inline comment
  openai.com
OPENAI.COM
`)
	got := ParseDomainList(data)
	want := []string{"google.com", "gstatic.com", "openai.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseDomainList = %v, want %v", got, want)
	}
}

func TestParseDomainList_Empty(t *testing.T) {
	if got := ParseDomainList([]byte("# only comments\n\n")); len(got) != 0 {
		t.Errorf("ParseDomainList = %v, want empty", got)
	}
}

func TestDecodeImport_INIConfig(t *testing.T) {
	data := []byte(`[settings]
mode = include
host = 127.0.0.1
port = 2080
service = Wi-Fi
pac_port = 21080

[proxy]
openai.com
*.githubusercontent.com
`)
	got, full, err := DecodeImport(data)
	if err != nil {
		t.Fatalf("DecodeImport: %v", err)
	}
	if !full {
		t.Error("DecodeImport: full = false, want true for an INI config")
	}
	if got.Mode != ModeInclude || got.Service != "Wi-Fi" {
		t.Errorf("DecodeImport(ini) = %+v", got)
	}
	if want := []string{"openai.com", "*.githubusercontent.com"}; !reflect.DeepEqual(got.Proxy, want) {
		t.Errorf("DecodeImport(ini) Proxy = %v, want %v", got.Proxy, want)
	}
}

func TestDecodeImport_INISyntaxError_DoesNotFallThrough(t *testing.T) {
	// Shaped like INI (has a [section] header) but has a typo'd key — must
	// surface the INI error, not silently fall back to YAML/plain-list.
	data := []byte(`[settings]
mde = include
`)
	_, _, err := DecodeImport(data)
	if err == nil {
		t.Fatal("DecodeImport: expected an error for a malformed INI payload")
	}
}

func TestDecodeImport_FullYAMLConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeInclude
	cfg.Proxy = []string{"google.com"}
	data, err := cfg.YAML()
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	got, full, err := DecodeImport(data)
	if err != nil {
		t.Fatalf("DecodeImport: %v", err)
	}
	if !full {
		t.Error("DecodeImport: full = false, want true for a YAML config")
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Errorf("DecodeImport config = %+v, want %+v", got, cfg)
	}
}

func TestDecodeImport_PlainDomainList(t *testing.T) {
	data := []byte("# proxy-domains.txt style paste\ngoogle.com\nopenai.com\n")
	got, full, err := DecodeImport(data)
	if err != nil {
		t.Fatalf("DecodeImport: %v", err)
	}
	if full {
		t.Error("DecodeImport: full = true, want false for a plain domain list")
	}
	want := []string{"google.com", "openai.com"}
	if !reflect.DeepEqual(got.Proxy, want) {
		t.Errorf("DecodeImport domains = %v, want %v", got.Proxy, want)
	}
}

func TestDecodeImport_EmptyIsError(t *testing.T) {
	if _, _, err := DecodeImport([]byte("# nothing but comments\n\n")); err == nil {
		t.Error("DecodeImport of an empty/comment-only payload should error")
	}
}

func TestConfig_ApplyImport_PlainListKeepsOtherFields(t *testing.T) {
	base := Config{
		Mode: ModeInclude, Host: "127.0.0.1", Port: 2080,
		Proxy: []string{"old.com"}, Service: "Wi-Fi", PACPort: 21080,
	}
	imported, full, err := DecodeImport([]byte("new.com\nother.com\n"))
	if err != nil {
		t.Fatalf("DecodeImport: %v", err)
	}
	merged := base.ApplyImport(imported, full)
	want := Config{
		Mode: ModeInclude, Host: "127.0.0.1", Port: 2080,
		Proxy: []string{"new.com", "other.com"}, Service: "Wi-Fi", PACPort: 21080,
	}
	if !reflect.DeepEqual(merged, want) {
		t.Errorf("ApplyImport (plain list) = %+v, want %+v", merged, want)
	}
}

func TestConfig_ApplyImport_FullConfigReplacesEverything(t *testing.T) {
	base := DefaultConfig()
	imported := Config{Mode: ModeExclude, Host: "10.0.0.1", Port: 3128, Service: "Ethernet", PACPort: 9999}
	merged := base.ApplyImport(imported, true)
	if !reflect.DeepEqual(merged, imported) {
		t.Errorf("ApplyImport (full config) = %+v, want %+v", merged, imported)
	}
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"default off", DefaultConfig(), false},
		{"invalid mode", Config{Mode: "bogus", Service: "Wi-Fi"}, true},
		{"off with no service", Config{Mode: ModeOff}, true},
		{"exclude ok", Config{Mode: ModeExclude, Host: "127.0.0.1", Port: 2080, Service: "Wi-Fi", PACPort: 21080}, false},
		{"exclude missing host", Config{Mode: ModeExclude, Port: 2080, Service: "Wi-Fi", PACPort: 21080}, true},
		{"exclude bad port", Config{Mode: ModeExclude, Host: "127.0.0.1", Port: 0, Service: "Wi-Fi", PACPort: 21080}, true},
		{"exclude bad pac_port", Config{Mode: ModeExclude, Host: "127.0.0.1", Port: 2080, Service: "Wi-Fi", PACPort: -1}, true},
		{"exclude pac_port 0 (OS-assigned) is valid", Config{Mode: ModeExclude, Host: "127.0.0.1", Port: 2080, Service: "Wi-Fi", PACPort: 0}, false},
		{"exclude port == pac_port", Config{Mode: ModeExclude, Host: "127.0.0.1", Port: 2080, Service: "Wi-Fi", PACPort: 2080}, true},
		{"exclude valid CIDR in direct", Config{Mode: ModeExclude, Host: "127.0.0.1", Port: 2080, Service: "Wi-Fi", PACPort: 21080, Direct: []string{"10.0.0.0/8"}}, false},
		{"exclude invalid CIDR in direct", Config{Mode: ModeExclude, Host: "127.0.0.1", Port: 2080, Service: "Wi-Fi", PACPort: 21080, Direct: []string{"10.0.0.0/abc"}}, true},
		{"exclude non-IPv4 CIDR in direct", Config{Mode: ModeExclude, Host: "127.0.0.1", Port: 2080, Service: "Wi-Fi", PACPort: 21080, Direct: []string{"::1/128"}}, true},
		{"exclude invalid CIDR in proxy", Config{Mode: ModeExclude, Host: "127.0.0.1", Port: 2080, Service: "Wi-Fi", PACPort: 21080, Proxy: []string{"1.2.3.4/99"}}, true},
		{"include ok", Config{Mode: ModeInclude, Host: "127.0.0.1", Port: 2080, Service: "Wi-Fi", PACPort: 21080, Proxy: []string{"google.com"}}, false},
		{"include no domains", Config{Mode: ModeInclude, Host: "127.0.0.1", Port: 2080, Service: "Wi-Fi", PACPort: 21080}, true},
		{"include blank-only domains", Config{Mode: ModeInclude, Host: "127.0.0.1", Port: 2080, Service: "Wi-Fi", PACPort: 21080, Proxy: []string{"  ", ""}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfig_ProxyDirective(t *testing.T) {
	cfg := Config{Host: "127.0.0.1", Port: 2080}
	if got, want := cfg.ProxyDirective(), "PROXY 127.0.0.1:2080"; got != want {
		t.Errorf("ProxyDirective() = %q, want %q", got, want)
	}
}

// TestDefaultConfig_MatchesOriginalHardcodedRules is the semantic
// no-regression proof behind DefaultDirectRules: every host the ORIGINAL
// hardcoded gen-exclude-pac.sh sent DIRECT is still DIRECT under
// DefaultConfig() alone (mode=exclude, nothing imported).
func TestDefaultConfig_MatchesOriginalHardcodedRules(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeExclude
	cfg.Host, cfg.Port = "127.0.0.1", 2080

	directHosts := []string{
		"printer", "yandex.ru", "ru", "почта.xn--p1ai", "myhost.local",
		"10.1.2.3", "172.16.5.5", "192.168.1.1", "100.64.0.5", "169.254.1.1", "127.0.0.1",
	}
	for _, h := range directHosts {
		if !MatchesExcludeDirect(cfg, h) {
			t.Errorf("MatchesExcludeDirect(DefaultConfig, %q) = false, want true (unchanged default behaviour)", h)
		}
	}
	proxiedHosts := []string{"openai.com", "example.com", "8.8.8.8", "public.example"}
	for _, h := range proxiedHosts {
		if MatchesExcludeDirect(cfg, h) {
			t.Errorf("MatchesExcludeDirect(DefaultConfig, %q) = true, want false (unchanged default behaviour)", h)
		}
	}
}
