package sysproxy

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseINI_ExampleTemplate(t *testing.T) {
	data, err := os.ReadFile("../../packaging/macos/singctl-proxy-rules.example.ini")
	if err != nil {
		t.Fatalf("read example template: %v", err)
	}
	cfg, err := ParseINI(data)
	if err != nil {
		t.Fatalf("ParseINI(example template): %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("example template validation: %v", err)
	}
	if cfg.Mode != ModeOff || len(cfg.Proxy) != 0 {
		t.Fatalf("example template must be inert with an empty proxy list: %+v", cfg)
	}
}

const sampleINI = `[settings]
mode = exclude          ; off | exclude | include
host = 127.0.0.1
port = 2080
service = Wi-Fi
pac_port = 21080

[proxy]                 ; force THROUGH the proxy
openai.com
*.githubusercontent.com

[direct]                ; never proxy
*.local
*.ru
10.0.0.0/8
100.64.0.0/10
`

func TestParseINI_Sample(t *testing.T) {
	cfg, err := ParseINI([]byte(sampleINI))
	if err != nil {
		t.Fatalf("ParseINI: %v", err)
	}
	want := Config{
		Mode: ModeExclude, Host: "127.0.0.1", Port: 2080, Service: "Wi-Fi", PACPort: 21080,
		BypassPlainHostnames: true, // omitted -> defaults true
		Proxy:                []string{"openai.com", "*.githubusercontent.com"},
		Direct:               []string{"*.local", "*.ru", "10.0.0.0/8", "100.64.0.0/10"},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("ParseINI(sample) = %+v, want %+v", cfg, want)
	}
}

func TestParseINI_RoundTrip(t *testing.T) {
	cfg := Config{
		Mode: ModeExclude, Host: "127.0.0.1", Port: 2080, Service: "Wi-Fi", PACPort: 21080,
		BypassPlainHostnames: false,
		Proxy:                []string{"openai.com", "*.githubusercontent.com"},
		Direct:               []string{"*.local", "10.0.0.0/8"},
	}
	data := cfg.INI()
	got, err := ParseINI(data)
	if err != nil {
		t.Fatalf("ParseINI: %v\n--- ini ---\n%s", err, data)
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Errorf("round trip mismatch:\n got  = %+v\n want = %+v\nini:\n%s", got, cfg, data)
	}
}

func TestParseINI_BypassPlainHostnamesExplicit(t *testing.T) {
	data := []byte(`[settings]
mode = off
host = 127.0.0.1
port = 2080
service = Wi-Fi
pac_port = 21080
bypass_plain_hostnames = false
`)
	cfg, err := ParseINI(data)
	if err != nil {
		t.Fatalf("ParseINI: %v", err)
	}
	if cfg.BypassPlainHostnames {
		t.Error("bypass_plain_hostnames = false should be respected, not silently defaulted to true")
	}
}

func TestParseINI_CaseInsensitiveKeysAndSections(t *testing.T) {
	data := []byte(`[SETTINGS]
MODE = off
Host = 127.0.0.1
PORT = 2080
Service = Wi-Fi
PAC_Port = 21080
`)
	cfg, err := ParseINI(data)
	if err != nil {
		t.Fatalf("ParseINI: %v", err)
	}
	if cfg.Mode != ModeOff || cfg.Service != "Wi-Fi" {
		t.Errorf("ParseINI(mixed case) = %+v", cfg)
	}
}

func TestParseINI_CommentsAndBlankLinesIgnored(t *testing.T) {
	data := []byte(`
; leading comment
[settings]
mode = off   # trailing comment
host = 127.0.0.1

; blank line above, comment here
port = 2080
service = Wi-Fi
pac_port = 21080
`)
	cfg, err := ParseINI(data)
	if err != nil {
		t.Fatalf("ParseINI: %v", err)
	}
	if cfg.Mode != ModeOff {
		t.Errorf("mode = %q, want off", cfg.Mode)
	}
}

// TestParseINI_SyntaxErrors covers every documented error case: unknown
// section, unknown key, malformed section header, missing "=", invalid mode/
// port/pac_port/bool value, invalid CIDR, non-IPv4 CIDR, entry outside any
// section, and a missing mode. Each error must name the offending line.
func TestParseINI_SyntaxErrors(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		namesLine bool // false only for whole-file errors with no single offending line
	}{
		{"unknown section", "[bogus]\nfoo\n", true},
		{"unknown settings key", "[settings]\nmde = off\n", true},
		{"malformed section header", "[settings\nmode = off\n", true},
		{"missing equals", "[settings]\nmode off\n", true},
		{"invalid mode", "[settings]\nmode = sideways\n", true},
		{"invalid port", "[settings]\nmode = off\nport = notanumber\n", true},
		{"invalid pac_port", "[settings]\nmode = off\npac_port = notanumber\n", true},
		{"invalid bool", "[settings]\nmode = off\nbypass_plain_hostnames = maybe\n", true},
		{"invalid CIDR in proxy", "[settings]\nmode = include\n[proxy]\n10.0.0.0/abc\n", true},
		{"non-IPv4 CIDR in direct", "[settings]\nmode = exclude\n[direct]\n::1/128\n", true},
		{"entry outside any section", "mode = off\n", true},
		{"missing mode", "[settings]\nhost = 127.0.0.1\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseINI([]byte(tt.data))
			if err == nil {
				t.Fatalf("ParseINI(%q): expected an error", tt.data)
			}
			if tt.namesLine && !strings.Contains(err.Error(), "line") {
				t.Errorf("error %q does not name a line", err.Error())
			}
		})
	}
}

func TestLooksLikeINI(t *testing.T) {
	if !looksLikeINI([]byte("; comment\n\n[settings]\nmode = off\n")) {
		t.Error("looksLikeINI(sample) = false, want true")
	}
	if looksLikeINI([]byte("mode: off\nhost: 127.0.0.1\n")) {
		t.Error("looksLikeINI(yaml) = true, want false")
	}
	if looksLikeINI([]byte("google.com\nopenai.com\n")) {
		t.Error("looksLikeINI(plain domain list) = true, want false")
	}
}

func TestConfig_INI_OmitsEmptySections(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeOff
	cfg.Direct = nil // strip DefaultConfig's Direct seed for this test
	data := string(cfg.INI())
	if strings.Contains(data, "[proxy]") {
		t.Error("INI() emitted an empty [proxy] section")
	}
	if strings.Contains(data, "[direct]") {
		t.Error("INI() emitted an empty [direct] section")
	}
}
