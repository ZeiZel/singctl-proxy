package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// envKey / envPort are the environment variables (also read from a .env file
// via godotenv) that provide defaults for --key and --port.
const (
	envKey  = "SINGCTL_KEY"
	envPort = "SINGCTL_PORT"
)

// options is the parsed command line. Precedence for key/port:
// flag > environment (SINGCTL_KEY / SINGCTL_PORT, optionally from .env) >
// saved profile (key only).
type options struct {
	key      string // -k/--key: vless:// link
	headless bool   // --headless: run without the TUI
	logs     bool   // -l/--logs: sing-box logs to stdout (TUI: open logs view)
	vpn      bool   // --vpn: start in VPN mode
	proxy    bool   // -p/--proxy: start in proxy mode
	port     int    // --port: local socks port (http = port+1); 0 = default 1080
	version  bool   // -v/--version
	man      bool   // --man: print the man page
	envFile  string // --env-file: explicit .env path (default: ./.env if present)
	noSave   bool   // --no-save: don't persist the link to the profile
}

// parseOptions parses args (without the program name). flag.ErrHelp is
// returned for -h/--help. Note: -h/--help and -v/--version keep the
// conventional short letters, so headless and vpn are long-only flags.
func parseOptions(args []string, out io.Writer) (*options, error) {
	o := &options{}
	fs := flag.NewFlagSet("singctl", flag.ContinueOnError)
	fs.SetOutput(out)

	fs.StringVar(&o.key, "k", "", "")
	fs.StringVar(&o.key, "key", "", "vless:// key (link) to load")
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

	fs.Usage = func() { fmt.Fprint(out, usageText) }
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if narg := fs.NArg(); narg > 0 {
		return nil, fmt.Errorf("unexpected argument %q (see --help)", fs.Arg(0))
	}
	return o, o.validate()
}

// applyEnv fills missing key/port from the environment (call after godotenv
// has loaded any .env file). Flags always win over the environment.
func (o *options) applyEnv(getenv func(string) string) error {
	if o.key == "" {
		o.key = strings.TrimSpace(getenv(envKey))
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
	return o.validate()
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
  -k, --key <vless://...>  vless key (link) to load
      --headless           run without the terminal UI (requires a key from
                           --key, $SINGCTL_KEY or the saved profile)
  -l, --logs               stream sing-box logs to stdout (headless) or open
                           the logs view on start (TUI)
      --vpn                enable VPN (TUN) mode on start
  -p, --proxy              enable proxy mode on start
      --port <n>           local SOCKS port (HTTP proxy listens on port+1;
                           defaults: 1080/2080)
      --env-file <path>    load environment variables from this file
                           (default: ./.env if present)
      --no-save            do not persist the key to ~/.config/singctl
      --man                print the man page (roff) and exit
  -v, --version            print version and exit
  -h, --help               show this help

Environment:
  SINGCTL_KEY              vless key, used when --key is not given
  SINGCTL_PORT             SOCKS port, used when --port is not given

Both variables can also be placed in a .env file in the working directory.
`
