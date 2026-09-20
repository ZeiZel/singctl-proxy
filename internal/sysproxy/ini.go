package sysproxy

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// The INI rules format
//
// This is the primary format for SYSPROXY-CONFIG/SYSPROXY-IMPORT (see
// DecodeImport for the other formats still accepted on import). A rules file
// looks like:
//
//	[settings]
//	mode = exclude          ; off | exclude | include
//	host = 127.0.0.1
//	port = 2080
//	service = Wi-Fi
//	pac_port = 21080
//	bypass_plain_hostnames = true   ; hosts with no dot (intranet, wiki) go DIRECT
//
//	[proxy]                 ; force THROUGH the proxy
//	openai.com
//	*.githubusercontent.com
//
//	[direct]                ; never proxy
//	*.local
//	10.0.0.0/8
//	100.64.0.0/10
//
// Grammar: blank lines are ignored; a comment starts with "#" or ";" (a
// full line, or trailing after content) and runs to end of line; section
// names and [settings] keys are case-insensitive; [settings] values are
// trimmed but not case-folded (so `service = Wi-Fi` keeps its case). An
// unknown section or an unknown [settings] key is an error naming the
// offending line — a typo must not silently half-apply.
//
// A [proxy]/[direct] entry is one of:
//   - a bare domain ("openai.com") — matches the domain itself and every
//     subdomain;
//   - a "*." (or any) wildcard containing "*" ("*.githubusercontent.com") —
//     a shell-glob pattern, matched the same way the PAC's own
//     shExpMatch would;
//   - a CIDR ("10.0.0.0/8") — matched only against a literal IPv4 host,
//     never triggers a DNS lookup. IPv6 CIDRs are rejected (unsupported).
//
// [settings]'s bypass_plain_hostnames is the one routing rule that can't be
// expressed as a [proxy]/[direct] entry: a bare/simple hostname (no dot —
// an intranet short name like "wiki") always goes DIRECT when true. It
// defaults to true (both DefaultConfig and here, when the key is omitted),
// so an imported file that doesn't mention it keeps today's behaviour rather
// than silently dropping the rule.
//
// PRECEDENCE: [proxy] always wins over [direct] — a host matching [proxy] is
// proxied even if it also matches [direct] (or bypass_plain_hostnames). This
// is the explicit override, and GenerateExcludePAC evaluates it FIRST, before
// any DIRECT rule — see that function and the generated PAC's own header
// comment, which restates this so a user reading the .pac file can predict
// it without reading this doc.
//
// mode = include proxies ONLY [proxy] (subdomains included); [direct] and
// bypass_plain_hostnames are not consulted at all in this mode. mode =
// exclude proxies everything except [direct] (and, if enabled,
// bypass_plain_hostnames), overridden by [proxy]. mode = off disables the
// PAC and the manual proxy, and clears the bypass-domain list — see
// Manager.Apply.
//
// Importing a full INI file REPLACES Direct entirely (see
// Config.ApplyImport) — DefaultConfig's DefaultDirectRules only apply until
// something is imported; from then on the imported [direct] section is the
// complete, exclusive source of truth for what stays direct.

// iniSections/iniSettingsKeys are the only section/key names ParseINI
// accepts; anything else is an error naming the line.
var iniSections = map[string]bool{
	"settings": true,
	"proxy":    true,
	"direct":   true,
}

var iniSettingsKeys = map[string]bool{
	"mode":                   true,
	"host":                   true,
	"port":                   true,
	"service":                true,
	"pac_port":               true,
	"bypass_plain_hostnames": true,
}

// looksLikeINI reports whether data's first non-blank, non-comment line is a
// "[section]" header — DecodeImport's shape check. Neither a legacy YAML
// Config nor a plain domain list starts a non-blank line with "[".
func looksLikeINI(data []byte) bool {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(stripIniComment(sc.Text()))
		if line == "" {
			continue
		}
		return strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]")
	}
	return false
}

// stripIniComment cuts line at the first "#" or ";", which starts a comment
// that runs to end of line (a full-line comment is just the degenerate case
// where that's the whole line).
func stripIniComment(line string) string {
	if i := strings.IndexAny(line, "#;"); i >= 0 {
		return line[:i]
	}
	return line
}

// iniLineError names the offending line (1-based) and its raw text, so a
// rules-file typo is easy to find.
func iniLineError(lineNo int, raw, msg string) error {
	return fmt.Errorf("sysproxy: ini: line %d: %s: %q", lineNo, msg, strings.TrimSpace(raw))
}

// ParseINI parses the INI rules format described above into a Config. It
// does not apply defaults — Mode and every [settings] key must be given
// explicitly except bypass_plain_hostnames, which defaults to true when
// omitted (see the format doc above). The caller is responsible for calling
// Validate (Manager.Apply/Import do this automatically) before applying —
// ParseINI itself only rejects what it can catch at parse time: an unknown
// section/key, a malformed "key = value" line, an invalid mode/port/bool
// value, and a syntactically invalid or non-IPv4 CIDR entry.
func ParseINI(data []byte) (Config, error) {
	cfg := Config{}
	haveMode := false
	haveBypass := false
	section := ""

	sc := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := sc.Text()
		line := strings.TrimSpace(stripIniComment(raw))
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return Config{}, iniLineError(lineNo, raw, "malformed section header")
			}
			name := strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if !iniSections[name] {
				return Config{}, iniLineError(lineNo, raw, fmt.Sprintf("unknown section %q", name))
			}
			section = name
			continue
		}

		switch section {
		case "":
			return Config{}, iniLineError(lineNo, raw, "entry outside any [section]")

		case "settings":
			key, val, ok := strings.Cut(line, "=")
			if !ok {
				return Config{}, iniLineError(lineNo, raw, "expected key = value")
			}
			key = strings.ToLower(strings.TrimSpace(key))
			val = strings.TrimSpace(val)
			if !iniSettingsKeys[key] {
				return Config{}, iniLineError(lineNo, raw, fmt.Sprintf("unknown key %q in [settings]", key))
			}
			switch key {
			case "mode":
				m := Mode(strings.ToLower(val))
				switch m {
				case ModeOff, ModeExclude, ModeInclude:
				default:
					return Config{}, iniLineError(lineNo, raw, fmt.Sprintf("invalid mode %q (want off, exclude or include)", val))
				}
				cfg.Mode = m
				haveMode = true
			case "host":
				cfg.Host = val
			case "port":
				p, err := strconv.Atoi(val)
				if err != nil {
					return Config{}, iniLineError(lineNo, raw, "invalid port "+val)
				}
				cfg.Port = p
			case "service":
				cfg.Service = val
			case "pac_port":
				p, err := strconv.Atoi(val)
				if err != nil {
					return Config{}, iniLineError(lineNo, raw, "invalid pac_port "+val)
				}
				cfg.PACPort = p
			case "bypass_plain_hostnames":
				b, err := strconv.ParseBool(val)
				if err != nil {
					return Config{}, iniLineError(lineNo, raw, "invalid bypass_plain_hostnames "+val)
				}
				cfg.BypassPlainHostnames = b
				haveBypass = true
			}

		case "proxy", "direct":
			if err := validateRuleEntrySyntax(line); err != nil {
				return Config{}, iniLineError(lineNo, raw, err.Error())
			}
			if section == "proxy" {
				cfg.Proxy = append(cfg.Proxy, line)
			} else {
				cfg.Direct = append(cfg.Direct, line)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return Config{}, fmt.Errorf("sysproxy: ini: %w", err)
	}
	if !haveMode {
		return Config{}, errors.New(`sysproxy: ini: missing "mode" in [settings]`)
	}
	if !haveBypass {
		cfg.BypassPlainHostnames = true
	}
	return cfg, nil
}

// validateRuleEntrySyntax rejects only what ParseINI can definitively call
// wrong at parse time: a "/"-bearing entry that isn't a valid IPv4 CIDR.
// Bare domains and glob patterns have no syntax to violate — anything else
// is accepted verbatim.
func validateRuleEntrySyntax(entry string) error {
	if !strings.Contains(entry, "/") {
		return nil
	}
	_, n, err := net.ParseCIDR(strings.ToLower(entry))
	if err != nil {
		return fmt.Errorf("invalid CIDR: %w", err)
	}
	if n.IP.To4() == nil {
		return errors.New("unsupported (non-IPv4) CIDR")
	}
	return nil
}

// INI renders c as the INI rules format above — the format SYSPROXY-CONFIG
// returns. Proxy/Direct entries are written one per line, verbatim (no
// case-folding, so a hand-authored file's exact text survives a round trip);
// empty [proxy]/[direct] sections are omitted entirely.
func (c Config) INI() []byte {
	var b strings.Builder
	b.WriteString("[settings]\n")
	fmt.Fprintf(&b, "mode = %s\n", c.Mode)
	fmt.Fprintf(&b, "host = %s\n", c.Host)
	fmt.Fprintf(&b, "port = %d\n", c.Port)
	fmt.Fprintf(&b, "service = %s\n", c.Service)
	fmt.Fprintf(&b, "pac_port = %d\n", c.PACPort)
	fmt.Fprintf(&b, "bypass_plain_hostnames = %t\n", c.BypassPlainHostnames)

	if len(c.Proxy) > 0 {
		b.WriteString("\n[proxy]\n")
		for _, e := range c.Proxy {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}
			fmt.Fprintf(&b, "%s\n", e)
		}
	}
	if len(c.Direct) > 0 {
		b.WriteString("\n[direct]\n")
		for _, e := range c.Direct {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}
			fmt.Fprintf(&b, "%s\n", e)
		}
	}
	return []byte(b.String())
}
