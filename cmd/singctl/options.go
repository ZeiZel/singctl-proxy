package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Environment variables (also read from a .env file via godotenv) that provide
// defaults for the corresponding flags.
const (
	envKey         = "SINGCTL_KEY"
	envKeys        = "SINGCTL_KEYS"
	envPort        = "SINGCTL_PORT"
	envClashAPI    = "SINGCTL_CLASH_API"
	envClashSecret = "SINGCTL_CLASH_SECRET"

	defaultClashAPI = "127.0.0.1:9090"
)

// stringList is a repeatable string flag: each occurrence appends a value, so
// -k can be given several times (failover priority follows the order).
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// options is the parsed command line. Precedence for keys/port:
// flag > environment (SINGCTL_KEY[S] / SINGCTL_PORT, optionally from .env) >
// saved profile (key only).
type options struct {
	keys     stringList // -k/--key: vless:// link(s), repeatable
	headless bool       // --headless: run without the TUI
	logs     bool       // -l/--logs: sing-box logs to stdout (TUI: open logs view)
	vpn      bool       // --vpn: start in VPN mode
	proxy    bool       // -p/--proxy: start in proxy mode
	port     int        // --port: local socks port (http = port+1); 0 = default 1080
	version  bool       // -v/--version
	man      bool       // --man: print the man page
	envFile  string     // --env-file: explicit .env path (default: ./.env if present)
	noSave   bool       // --no-save: don't persist the link to the profile

	clashAPI    string // --clash-api: Clash API address (host:port); "" via --no-clash-api
	noClash     bool   // --no-clash-api: disable the Clash API
	clashSecret string // --clash-secret: Clash API secret (default: random)

	urltestURL       string // --urltest-url: failover probe URL
	urltestInterval  string // --urltest-interval: failover probe interval
	urltestTolerance int    // --urltest-tolerance: failover switch hysteresis (ms)
}

// parseOptions parses args (without the program name). flag.ErrHelp is
// returned for -h/--help. Note: -h/--help and -v/--version keep the
// conventional short letters, so headless and vpn are long-only flags.
func parseOptions(args []string, out io.Writer) (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet("singctl", flag.ContinueOnError)
	fs.SetOutput(out)

	fs.Var(&o.keys, "k", "")
	fs.Var(&o.keys, "key", "vless:// key (link) to load; repeat for failover")
	fs.BoolVar(&o.headless, "headless", false, "run without the terminal UI")
	fs.BoolVar(&o.logs, "l", false, "")
	fs.BoolVar(&o.logs, "logs", false, "stream sing-box logs to stdout")
	fs.BoolVar(&o.vpn, "vpn", false, "enable VPN (TUN) mode on start")
	fs.BoolVar(&o.proxy, "p", false, "")
	fs.BoolVar(&o.proxy, "proxy", false, "enable proxy mode on start")
	fs.IntVar(&o.port, "port", 0, "local SOCKS proxy port (HTTP listens on port+1)")
	fs.BoolVar(&o.version, "v", false, "")
	fs.BoolVar(&o.version, "version", false, "print version and exit")
	fs.BoolVar(&o.man, "man", false, "print the man page (roff) and exit")
	fs.StringVar(&o.envFile, "env-file", "", "load environment from this file (default: ./.env if present)")
	fs.BoolVar(&o.noSave, "no-save", false, "do not persist the key to the profile")

	fs.StringVar(&o.clashAPI, "clash-api", defaultClashAPI, "Clash API address host:port (connection logging + latency)")
	fs.BoolVar(&o.noClash, "no-clash-api", false, "disable the Clash API")
	fs.StringVar(&o.clashSecret, "clash-secret", "", "Clash API secret (default: random per run)")
	fs.StringVar(&o.urltestURL, "urltest-url", "", "failover probe URL (default: gstatic generate_204)")
	fs.StringVar(&o.urltestInterval, "urltest-interval", "", "failover probe interval (default: 3m)")
	fs.IntVar(&o.urltestTolerance, "urltest-tolerance", 0, "failover switch hysteresis in ms (default: 50)")

	fs.Usage = func() { fmt.Fprint(out, usageText) }
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if narg := fs.NArg(); narg > 0 {
		return nil, fmt.Errorf("unexpected argument %q (see --help)", fs.Arg(0))
	}
	return o, o.validate()
}

// applyEnv fills missing keys/port/clash settings from the environment (call
// after godotenv has loaded any .env file). Flags always win over the
// environment.
func (o *options) applyEnv(getenv func(string) string) error {
	if len(o.keys) == 0 {
		if v := strings.TrimSpace(getenv(envKeys)); v != "" {
			o.keys = append(o.keys, v) // ParseLinks splits a multi-link blob
		} else if v := strings.TrimSpace(getenv(envKey)); v != "" {
			o.keys = append(o.keys, v)
		}
	}
	if o.port == 0 {
		if raw := strings.TrimSpace(getenv(envPort)); raw != "" {
			p, err := strconv.Atoi(raw)
			if err != nil {
				return fmt.Errorf("%s: invalid port %q", envPort, raw)
			}
			o.port = p
		}
	}
	if o.clashSecret == "" {
		o.clashSecret = strings.TrimSpace(getenv(envClashSecret))
	}
	// Env overrides the address only when the flag is still at its default.
	if v := strings.TrimSpace(getenv(envClashAPI)); v != "" && o.clashAPI == defaultClashAPI {
		o.clashAPI = v
	}
	return o.validate()
}

// combinedKey joins the configured links into one newline-separated blob, which
// vless.ParseLinks splits back into the failover set.
func (o *options) combinedKey() string {
	return strings.Join(o.keys, "\n")
}

// effectiveClashAPI returns the Clash API address, or "" when disabled.
func (o *options) effectiveClashAPI() string {
	if o.noClash {
		return ""
	}
	return o.clashAPI
}

func (o *options) validate() error {
	if o.vpn && o.proxy {
		return fmt.Errorf("--vpn and --proxy are mutually exclusive")
	}
	// port+1 is the http listener, so 65534 is the highest usable socks port.
	if o.port != 0 && (o.port < 1 || o.port > 65534) {
		return fmt.Errorf("--port must be in range 1..65534, got %d", o.port)
	}
	// --headless without a key is checked in main: the saved profile may still
	// supply the link after this point.
	return nil
}

const usageText = `singctl — VLESS proxy / VPN client on an embedded sing-box core

Usage:
  sudo singctl [flags]

Flags:
  -k, --key <vless://...>  vless key (link) to load; repeat to add failover
                           servers (fastest reachable is picked automatically)
      --headless           run without the terminal UI (requires a key from
                           --key, $SINGCTL_KEY or the saved profile)
  -l, --logs               stream sing-box logs to stdout (headless) or open
                           the logs view on start (TUI)
      --vpn                enable VPN (TUN) mode on start
  -p, --proxy              enable proxy mode on start
      --port <n>           local SOCKS port (HTTP proxy listens on port+1;
                           defaults: 1080/2080)
      --clash-api <a:p>    Clash API address for connection logging + latency
                           (default: 127.0.0.1:9090)
      --no-clash-api       disable the Clash API
      --clash-secret <s>   Clash API secret (default: random per run)
      --urltest-url <url>  failover probe URL (default: gstatic generate_204)
      --urltest-interval <d>  failover probe interval (default: 3m)
      --urltest-tolerance <ms> failover switch hysteresis (default: 50)
      --env-file <path>    load environment variables from this file
                           (default: ./.env if present)
      --no-save            do not persist the key to ~/.config/singctl
      --man                print the man page (roff) and exit
  -v, --version            print version and exit
  -h, --help               show this help

Environment:
  SINGCTL_KEY             vless key, used when --key is not given
  SINGCTL_KEYS            several newline/;-separated vless keys (failover)
  SINGCTL_PORT            SOCKS port, used when --port is not given
  SINGCTL_CLASH_API       Clash API address, used when --clash-api is default
  SINGCTL_CLASH_SECRET    Clash API secret, used when --clash-secret is unset

These variables can also be placed in a .env file in the working directory.
`
