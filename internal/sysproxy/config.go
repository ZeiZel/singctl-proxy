package sysproxy

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"gopkg.in/yaml.v3"
)

// Mode selects how the system-proxy toggle behaves. It mirrors the Makefile's
// three states (no toggle applied / `proxy-on` / `proxy-pac`).
type Mode string

const (
	// ModeOff disables both the manual web proxy and the PAC-driven automatic
	// proxy on the configured network service, and clears the bypass-domain
	// list. It is the safe, inert default — see the package doc.
	ModeOff Mode = "off"
	// ModeExclude proxies EVERYTHING except what Direct matches (plus, when
	// BypassPlainHostnames is set, bare/simple hostnames) — UNLESS a host
	// also matches Proxy, which always wins. Mirrors `make proxy-on`.
	ModeExclude Mode = "exclude"
	// ModeInclude proxies ONLY the rules listed in Proxy (and their
	// subdomains); everything else goes DIRECT. Direct is not consulted in
	// this mode. Mirrors `make proxy-pac`.
	ModeInclude Mode = "include"
)

// Defaults mirror the Makefile's PROXY_SERVICE/PROXY_HOST/PROXY_PORT (see the
// "--- macOS system proxy toggle ---" section), so a fresh Config behaves the
// same way `make proxy-on`/`proxy-pac` would out of the box.
const (
	DefaultService   = "Wi-Fi"
	DefaultHost      = "127.0.0.1"
	DefaultProxyPort = 2080
)

// LegacyPACPort is the port the standalone mac-proxy utility's LaunchAgent
// (and singctl's own pre-2.0 `make pac-server` target) hardcoded — 21080. It
// is NOT DefaultConfig's PACPort any more (see F1 in docs/v2-spec.md): the two
// tools shared this port under the same LaunchAgent label
// (com.singctl.pacserver), so whichever bound it second failed with an
// unactionable "address already in use". DefaultConfig now leaves PACPort at
// 0 (OS-assigned, never collides); this constant is kept only for
// documentation/tests and for a caller that deliberately wants the old fixed
// port back (which still risks the exact conflict this default now avoids).
const LegacyPACPort = 21080

// DefaultDirectRules are generic private/link-local/loopback ranges and local
// hostnames. Organization-specific bypass rules belong in each user's local
// configuration and are never part of the shipped defaults.
var DefaultDirectRules = []string{
	"10.0.0.0/8",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"100.64.0.0/10",  // CGNAT
	"169.254.0.0/16", // link-local
	"127.0.0.0/8",
	"*.ru",
	"*.xn--p1ai",
	"*.local",
}

// Config is the full, serializable description of the system-proxy toggle.
//
// The zero value is NOT a safe "off" config to Apply (Service/Host/PACPort
// are empty, which fails Validate) — start from DefaultConfig(), or a loaded
// one, and only override what you mean to change.
type Config struct {
	// Mode selects off/exclude/include. Required.
	Mode Mode `yaml:"mode" json:"mode"`
	// Host/Port are the local proxy singctl runs; together they form the
	// PAC's PROXY directive (see ProxyDirective). Ignored in ModeOff.
	Host string `yaml:"host" json:"host"`
	Port int    `yaml:"port" json:"port"`
	// Service is the macOS network service the toggle applies to (see
	// `networksetup -listallnetworkservices`), e.g. "Wi-Fi". Required in
	// every mode — ModeOff still needs to know which service to disable on.
	Service string `yaml:"service" json:"service"`
	// PACPort is the localhost port the generated PAC is served from. Ignored
	// in ModeOff. 0 means "let the OS assign an ephemeral port" — the actual
	// bound port is always resolved via Manager.Status().PACURL, never
	// re-derived from this field, so 0 is a legitimate steady-state value,
	// not just a testing convenience.
	PACPort int `yaml:"pac_port" json:"pac_port"`
	// BypassPlainHostnames controls the one remaining hardcoded routing rule:
	// a bare/simple hostname (no dot — an intranet short name like "wiki" or
	// "jira") always goes DIRECT when true. Defaults to true (DefaultConfig,
	// and ParseINI when the key is omitted) so behaviour is unchanged for
	// anyone who doesn't set it. This is the one rule that genuinely cannot
	// be expressed as a [proxy]/[direct] entry (domain/wildcard/CIDR all
	// require a dot or a slash), so it stays a named [settings] toggle
	// instead — see ParseINI's doc comment.
	BypassPlainHostnames bool `yaml:"bypass_plain_hostnames" json:"bypass_plain_hostnames"`
	// Proxy is the explicit "force THROUGH the proxy" rule list — the INI
	// [proxy] section. In ModeInclude it is the only thing that's ever
	// proxied (everything else is DIRECT). In ModeExclude it's an override:
	// a host matching Proxy is proxied even if it also matches Direct or the
	// bare-hostname rule — see GenerateExcludePAC. Each entry is a bare
	// domain (matches itself and its subdomains), a shell-glob pattern
	// (anything containing "*", e.g. "*.githubusercontent.com"), or a CIDR
	// (matched only against literal IPv4
	// hosts). Ignored in ModeOff.
	Proxy []string `yaml:"proxy,omitempty" json:"proxy,omitempty"`
	// Direct is the "never proxy" rule list — the INI [direct] section. Only
	// consulted in ModeExclude (ModeInclude ignores it entirely — "only
	// [proxy] is proxied"), and only for hosts that don't also match Proxy.
	// Same entry grammar as Proxy. DefaultConfig seeds this with
	// DefaultDirectRules.
	Direct []string `yaml:"direct,omitempty" json:"direct,omitempty"`
}

// DefaultConfig returns the inert ModeOff config with the Makefile's default
// proxy/service values filled in, PACPort left at 0 (OS-assigned — see F1 in
// docs/v2-spec.md: a fixed default port is what let singctl's in-process PAC
// server collide with the standalone mac-proxy utility's LaunchAgent, which
// hardcodes the same port under the same LaunchAgent label), and
// Direct/BypassPlainHostnames seeded with generic private-network defaults,
// ready to be overridden by a user-owned configuration.
func DefaultConfig() Config {
	return Config{
		Mode:                 ModeOff,
		Host:                 DefaultHost,
		Port:                 DefaultProxyPort,
		Service:              DefaultService,
		PACPort:              0,
		BypassPlainHostnames: true,
		Direct:               append([]string(nil), DefaultDirectRules...),
	}
}

// ProxyDirective renders the PAC "PROXY host:port" directive, matching the
// Makefile's `"PROXY $(PROXY_HOST):$(PROXY_PORT)"`.
func (c Config) ProxyDirective() string {
	return fmt.Sprintf("PROXY %s:%d", c.Host, c.Port)
}

// Validate reports whether c is complete enough to Apply. It never mutates c
// or touches the system — Manager.Apply calls it first and applies nothing at
// all when it fails, so a malformed import (bad CIDR, bad mode, ...) never
// half-applies.
func (c Config) Validate() error {
	switch c.Mode {
	case ModeOff, ModeExclude, ModeInclude:
	default:
		return fmt.Errorf("sysproxy: invalid mode %q (want %q, %q or %q)", c.Mode, ModeOff, ModeExclude, ModeInclude)
	}
	if strings.TrimSpace(c.Service) == "" {
		return errors.New("sysproxy: service is required")
	}
	if c.Mode == ModeOff {
		return nil
	}
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("sysproxy: host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("sysproxy: invalid port %d", c.Port)
	}
	if c.PACPort < 0 || c.PACPort > 65535 {
		return fmt.Errorf("sysproxy: invalid pac_port %d", c.PACPort)
	}
	if c.PACPort != 0 && c.PACPort == c.Port {
		return errors.New("sysproxy: pac_port must differ from the proxy port")
	}
	if err := validateRules(c.Proxy); err != nil {
		return fmt.Errorf("sysproxy: [proxy] %w", err)
	}
	if err := validateRules(c.Direct); err != nil {
		return fmt.Errorf("sysproxy: [direct] %w", err)
	}
	if c.Mode == ModeInclude && len(normalizeDomains(c.Proxy)) == 0 {
		return errors.New("sysproxy: include mode requires at least one [proxy] rule")
	}
	return nil
}

// validateRules rejects a rule list containing a syntactically invalid or
// unsupported (non-IPv4) CIDR entry — the one shape sysproxy can't just treat
// as an opaque bare domain/glob, so a typo there must fail loudly rather than
// silently matching nothing.
func validateRules(rules []string) error {
	for _, raw := range rules {
		e := strings.TrimSpace(raw)
		if e == "" || !strings.Contains(e, "/") {
			continue
		}
		_, n, err := net.ParseCIDR(strings.ToLower(e))
		if err != nil {
			return fmt.Errorf("invalid CIDR %q: %w", raw, err)
		}
		if n.IP.To4() == nil {
			return fmt.Errorf("unsupported (non-IPv4) CIDR %q", raw)
		}
	}
	return nil
}

// yamlConfig is Config's YAML wire shape. It accepts the pre-INI field names
// (domains/ru_domains) as aliases for Proxy/Direct on decode — see
// ParseConfigYAML — so YAML written before this package grew an INI format
// still imports cleanly; they are never emitted by Config.YAML.
type yamlConfig struct {
	Mode                 Mode     `yaml:"mode"`
	Host                 string   `yaml:"host"`
	Port                 int      `yaml:"port"`
	Service              string   `yaml:"service"`
	PACPort              int      `yaml:"pac_port"`
	BypassPlainHostnames bool     `yaml:"bypass_plain_hostnames"`
	Proxy                []string `yaml:"proxy,omitempty"`
	Direct               []string `yaml:"direct,omitempty"`
	// Legacy pre-INI aliases, decode-only.
	Domains   []string `yaml:"domains,omitempty"`
	RUDomains []string `yaml:"ru_domains,omitempty"`
}

// YAML renders c as YAML (gopkg.in/yaml.v3). Kept for backward-compatible
// import (see DecodeImport) — SYSPROXY-CONFIG now returns INI (Config.INI),
// the primary format going forward.
func (c Config) YAML() ([]byte, error) {
	return yaml.Marshal(yamlConfig{
		Mode:                 c.Mode,
		Host:                 c.Host,
		Port:                 c.Port,
		Service:              c.Service,
		PACPort:              c.PACPort,
		BypassPlainHostnames: c.BypassPlainHostnames,
		Proxy:                c.Proxy,
		Direct:               c.Direct,
	})
}

// ParseConfigYAML parses YAML produced by Config.YAML (or hand-written in the
// same shape, including the legacy domains/ru_domains keys) back into a
// Config. It does not apply defaults or validate — callers that need a
// complete, appliable Config should merge onto DefaultConfig() or a
// previously-applied one first.
func ParseConfigYAML(data []byte) (Config, error) {
	var y yamlConfig
	if err := yaml.Unmarshal(data, &y); err != nil {
		return Config{}, err
	}
	proxy := y.Proxy
	if len(proxy) == 0 {
		proxy = y.Domains
	}
	direct := y.Direct
	if len(direct) == 0 {
		direct = y.RUDomains
	}
	return Config{
		Mode:                 y.Mode,
		Host:                 y.Host,
		Port:                 y.Port,
		Service:              y.Service,
		PACPort:              y.PACPort,
		BypassPlainHostnames: y.BypassPlainHostnames,
		Proxy:                proxy,
		Direct:               direct,
	}, nil
}

// ParseDomainList parses a plain newline-separated domain list — exactly the
// format packaging/macos/proxy-domains.txt and ru-extra.txt use: one
// registrable domain per line, "#" starts a comment (inline or full-line),
// blank lines are ignored, matching the awk normalization gen-pac.sh and
// gen-exclude-pac.sh apply before building the PAC's lookup object.
// Duplicates are dropped (order-preserving); this changes nothing observable
// since the generated PAC's lookup object would dedupe them anyway.
func ParseDomainList(data []byte) []string {
	var out []string
	seen := make(map[string]bool)
	for _, raw := range strings.Split(string(data), "\n") {
		line := raw
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.Map(func(r rune) rune {
			switch r {
			case ' ', '\t', '\r':
				return -1
			default:
				return r
			}
		}, line)
		line = strings.ToLower(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out
}

// DecodeImport decodes a SYSPROXY-IMPORT payload, which may be an INI Config
// (see ParseINI — the primary, preferred format), a full YAML Config (the
// pre-INI format, still accepted), or a plain newline-separated domain list
// (see ParseDomainList — the format a user pastes
// packaging/macos/proxy-domains.txt-style content in as). The shape is
// detected in that order:
//
//  1. If the payload's first non-blank/non-comment line is a "[section]"
//     header, it's parsed as INI. A syntax error here is returned as-is
//     (never silently falls through to the next format) — see ParseINI.
//  2. Otherwise, if it parses as YAML AND sets Mode, it's a full legacy YAML
//     config (a plain domain list does not parse this way: each line is a
//     bare scalar, which YAML folds into a single top-level string and
//     yaml.Unmarshal then rejects for a struct target).
//  3. Otherwise it's parsed as a plain domain list into a Proxy-only Config
//     (full is false), erroring only if that list also comes back empty.
//
// full is true for INI and full YAML (the whole config is replaced — see
// Config.ApplyImport); false for a plain domain list (only Proxy is
// replaced).
func DecodeImport(data []byte) (cfg Config, full bool, err error) {
	if looksLikeINI(data) {
		c, err := ParseINI(data)
		if err != nil {
			return Config{}, false, err
		}
		return c, true, nil
	}

	c, yerr := ParseConfigYAML(data)
	if yerr == nil && c.Mode != "" {
		return c, true, nil
	}
	domains := ParseDomainList(data)
	if len(domains) == 0 {
		if yerr != nil {
			return Config{}, false, fmt.Errorf("sysproxy: not a valid YAML config (%w) and no domains found as a plain list", yerr)
		}
		return Config{}, false, errors.New("sysproxy: empty import")
	}
	return Config{Proxy: domains}, false, nil
}

// ApplyImport merges an imported Config (from DecodeImport) onto the
// receiver, which is treated as the current/base config. A full config
// (full == true — INI or legacy YAML) replaces the base entirely; a
// domain-list-only import (full == false) keeps every other field of the
// base and replaces only Proxy — pasting proxy-domains.txt-style content
// updates the include-mode allowlist without disturbing
// mode/host/port/service/Direct.
func (base Config) ApplyImport(imported Config, full bool) Config {
	if full {
		return imported
	}
	out := base
	out.Proxy = imported.Proxy
	return out
}

// normalizeDomains lowercases, trims, and dedupes a rule list
// (order-preserving), the same normalization the PAC generators/matchers
// apply. Shared so Config.Validate's non-empty check and the PAC
// generators/matchers agree on what counts as "a rule." Despite the name it
// operates on any Proxy/Direct entry (domain, glob, or CIDR) — the "domains"
// name predates the INI format's richer grammar and is kept to avoid
// pointless churn.
func normalizeDomains(domains []string) []string {
	seen := make(map[string]bool, len(domains))
	out := make([]string, 0, len(domains))
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}
