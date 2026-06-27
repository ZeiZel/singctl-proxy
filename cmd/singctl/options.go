package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"singctl/internal/clashapi"
	"singctl/internal/control"
	"singctl/internal/feature"
	"singctl/internal/license"
	"singctl/internal/proclist"
	"singctl/internal/procproxy"
	"singctl/internal/runtime"
	"singctl/internal/ui"
	"singctl/internal/vless"
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

// --- feature modules: each owns its flags + parsed values + a descriptor ---

// keysModule: -k/--key, --no-save (feature: vless keys).
type keysModule struct {
	keys   stringList
	noSave bool
}

func (m *keysModule) Descriptor() feature.Descriptor { return vless.FeatureDescriptor() }
func (m *keysModule) Bind(fs *flag.FlagSet) {
	fs.Var(&m.keys, "k", "")
	fs.Var(&m.keys, "key", "vless:// key; repeat for failover")
	fs.BoolVar(&m.noSave, "no-save", false, "do not persist the key")
}
func (m *keysModule) applyEnv(getenv func(string) string) {
	if len(m.keys) > 0 {
		return
	}
	if v := strings.TrimSpace(getenv(envKeys)); v != "" {
		m.keys = append(m.keys, v) // ParseLinks splits a multi-link blob
		return
	}
	if v := strings.TrimSpace(getenv(envKey)); v != "" {
		m.keys = append(m.keys, v)
	}
}
func (m *keysModule) combined() string { return strings.Join(m.keys, "\n") }

// proxyModule: -p/--proxy, --vpn, --port, --headless, -l/--logs (feature: mode).
type proxyModule struct {
	proxy    bool
	vpn      bool
	headless bool
	logs     bool
	daemon   bool
	port     int
}

func (m *proxyModule) Descriptor() feature.Descriptor { return runtime.FeatureDescriptor() }
func (m *proxyModule) Bind(fs *flag.FlagSet) {
	fs.BoolVar(&m.proxy, "p", false, "")
	fs.BoolVar(&m.proxy, "proxy", false, "enable proxy mode on start")
	fs.BoolVar(&m.vpn, "vpn", false, "enable VPN (TUN) mode on start")
	fs.IntVar(&m.port, "port", 0, "local SOCKS proxy port (HTTP listens on port+1)")
	fs.BoolVar(&m.headless, "headless", false, "run without the terminal UI")
	fs.BoolVar(&m.logs, "l", false, "")
	fs.BoolVar(&m.logs, "logs", false, "stream sing-box logs to stdout")
	fs.BoolVar(&m.daemon, "daemon", false, "run detached in the background and exit (manage with --status/--stop)")
}
func (m *proxyModule) applyEnv(getenv func(string) string) error {
	if m.port == 0 {
		if raw := strings.TrimSpace(getenv(envPort)); raw != "" {
			p, err := strconv.Atoi(raw)
			if err != nil {
				return fmt.Errorf("%s: invalid port %q", envPort, raw)
			}
			m.port = p
		}
	}
	return nil
}
func (m *proxyModule) validate() error {
	if m.vpn && m.proxy {
		return fmt.Errorf("--vpn and --proxy are mutually exclusive")
	}
	// port+1 is the http listener, so 65534 is the highest usable socks port.
	if m.port != 0 && (m.port < 1 || m.port > 65534) {
		return fmt.Errorf("--port must be in range 1..65534, got %d", m.port)
	}
	return nil
}

// obsModule: --clash-api, --no-clash-api, --clash-secret, --urltest-* (Clash API).
type obsModule struct {
	clashAPI         string
	noClash          bool
	clashSecret      string
	urltestURL       string
	urltestInterval  string
	urltestTolerance int
}

func (m *obsModule) Descriptor() feature.Descriptor { return clashapi.FeatureDescriptor() }
func (m *obsModule) Bind(fs *flag.FlagSet) {
	fs.StringVar(&m.clashAPI, "clash-api", defaultClashAPI, "Clash API address host:port (connection logging + latency)")
	fs.BoolVar(&m.noClash, "no-clash-api", false, "disable the Clash API")
	fs.StringVar(&m.clashSecret, "clash-secret", "", "Clash API secret (default: random per run)")
	fs.StringVar(&m.urltestURL, "urltest-url", "", "failover probe URL (default: gstatic generate_204)")
	fs.StringVar(&m.urltestInterval, "urltest-interval", "", "failover probe interval (default: 3m)")
	fs.IntVar(&m.urltestTolerance, "urltest-tolerance", 0, "failover switch hysteresis in ms (default: 50)")
}
func (m *obsModule) applyEnv(getenv func(string) string) {
	if m.clashSecret == "" {
		m.clashSecret = strings.TrimSpace(getenv(envClashSecret))
	}
	// Env overrides the address only when the flag is still at its default.
	if v := strings.TrimSpace(getenv(envClashAPI)); v != "" && m.clashAPI == defaultClashAPI {
		m.clashAPI = v
	}
}
func (m *obsModule) effectiveClashAPI() string {
	if m.noClash {
		return ""
	}
	return m.clashAPI
}

// procModule: --route-pid, --restart-pid, --launch (per-process proxying).
type procModule struct {
	routePIDRaw   stringList
	restartPIDRaw stringList
	launch        bool
	launchArgv    []string
}

func (m *procModule) Descriptor() feature.Descriptor { return procproxy.FeatureDescriptor() }
func (m *procModule) Bind(fs *flag.FlagSet) {
	fs.Var(&m.routePIDRaw, "route-pid", "PID to route through the proxy (Linux; repeatable)")
	fs.Var(&m.restartPIDRaw, "restart-pid", "PID to restart in proxy mode (repeatable)")
	fs.BoolVar(&m.launch, "launch", false, "run the trailing command through the proxy (use: --launch -- cmd args)")
}
func (m *procModule) routePIDs() ([]int, error)   { return parsePIDs("--route-pid", m.routePIDRaw) }
func (m *procModule) restartPIDs() ([]int, error) { return parsePIDs("--restart-pid", m.restartPIDRaw) }
func (m *procModule) validate() error {
	if m.launch && len(m.launchArgv) == 0 {
		return fmt.Errorf("--launch needs a command, e.g. --launch -- curl https://...")
	}
	if _, err := m.routePIDs(); err != nil {
		return err
	}
	if _, err := m.restartPIDs(); err != nil {
		return err
	}
	return nil
}

// controlModule: --attach, --stop, --status (manage a running instance).
type controlModule struct {
	attach bool
	stop   bool
	status bool
}

func (m *controlModule) Descriptor() feature.Descriptor { return control.FeatureDescriptor() }
func (m *controlModule) Bind(fs *flag.FlagSet) {
	fs.BoolVar(&m.attach, "attach", false, "follow the logs of a running singctl instance")
	fs.BoolVar(&m.stop, "stop", false, "stop a running singctl instance")
	fs.BoolVar(&m.status, "status", false, "print the status of a running singctl instance")
}

// licenseModule: --license (install token/file), --license-status.
type licenseModule struct {
	install string
	status  bool
}

func (m *licenseModule) Descriptor() feature.Descriptor { return license.FeatureDescriptor() }
func (m *licenseModule) Bind(fs *flag.FlagSet) {
	fs.StringVar(&m.install, "license", "", "install a license (token or path to a file) and exit")
	fs.BoolVar(&m.status, "license-status", false, "print license status and exit")
}

// rootModule: global flags --version/-v, --man, --env-file.
type rootModule struct {
	version bool
	man     bool
	envFile string
	yes     bool
}

func (m *rootModule) Descriptor() feature.Descriptor {
	return feature.Descriptor{
		Name: "global", Title: "Общие", Summary: "версия / справка / окружение",
		Flags: []feature.FlagSpec{
			{Names: []string{"v", "version"}, Usage: "показать версию и выйти"},
			{Names: []string{"man"}, Usage: "напечатать man-страницу и выйти"},
			{Names: []string{"y", "yes"}, Usage: "не спрашивать подтверждение для опасных действий (--stop/--restart-pid)"},
			{Names: []string{"env-file"}, Placeholder: "<path>", Usage: "загрузить переменные окружения из файла (по умолчанию ./.env)"},
		},
	}
}
func (m *rootModule) Bind(fs *flag.FlagSet) {
	fs.BoolVar(&m.version, "v", false, "")
	fs.BoolVar(&m.version, "version", false, "print version and exit")
	fs.BoolVar(&m.man, "man", false, "print the man page (roff) and exit")
	fs.BoolVar(&m.yes, "y", false, "")
	fs.BoolVar(&m.yes, "yes", false, "skip confirmation prompts for destructive actions")
	fs.StringVar(&m.envFile, "env-file", "", "load environment from this file (default: ./.env if present)")
}

// docModule is a doc-only module (no flags): contributes a descriptor for help/man.
type docModule struct{ d feature.Descriptor }

func (m docModule) Descriptor() feature.Descriptor { return m.d }
func (m docModule) Bind(*flag.FlagSet)             {}

// --- aggregate CLI ---

// cli aggregates the feature modules and the registry that documents + binds them.
type cli struct {
	reg   *feature.Registry
	keys  keysModule
	proxy proxyModule
	obs   obsModule
	proc  procModule
	ctl   controlModule
	lic   licenseModule
	root  rootModule
}

// buildRegistry wires the modules into a registry in help-display order.
func (c *cli) buildRegistry() {
	c.reg = feature.New("singctl",
		"VLESS proxy / VPN client on an embedded sing-box core",
		"sudo singctl [flags]").
		Add(&c.keys).
		Add(&c.proxy).
		Add(&c.obs).
		Add(&c.proc).
		Add(&c.ctl).
		Add(&c.lic).
		Add(&c.root).
		Add(docModule{proclist.FeatureDescriptor()}).
		Add(docModule{ui.FeatureDescriptor()})
}

// parseCLI builds the registry, binds + parses flags, captures trailing argv and
// validates. flag.ErrHelp is returned for -h/--help.
func parseCLI(args []string, out io.Writer) (*cli, error) {
	c := &cli{}
	c.buildRegistry()
	fs := flag.NewFlagSet("singctl", flag.ContinueOnError)
	fs.SetOutput(out)
	c.reg.Bind(fs)
	fs.Usage = func() { c.reg.Help(out) }
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	// Trailing args (after `--launch --`) form the command to launch.
	c.proc.launchArgv = fs.Args()
	if !c.proc.launch && len(c.proc.launchArgv) > 0 {
		return nil, fmt.Errorf("unexpected argument %q (see --help)", c.proc.launchArgv[0])
	}
	return c, c.validate()
}

// applyEnv fills missing settings from the environment (flags always win).
func (c *cli) applyEnv(getenv func(string) string) error {
	c.keys.applyEnv(getenv)
	if err := c.proxy.applyEnv(getenv); err != nil {
		return err
	}
	c.obs.applyEnv(getenv)
	return c.validate()
}

func (c *cli) validate() error {
	if err := c.proxy.validate(); err != nil {
		return err
	}
	if c.proxy.daemon {
		if c.ctl.attach || c.ctl.stop || c.ctl.status {
			return fmt.Errorf("--daemon cannot be combined with --attach/--stop/--status")
		}
		if c.keys.noSave {
			return fmt.Errorf("--daemon needs a saved key (the background process loads it); drop --no-save")
		}
	}
	return c.proc.validate()
}

func parsePIDs(flagName string, raw stringList) ([]int, error) {
	pids := make([]int, 0, len(raw))
	for _, r := range raw {
		p, err := strconv.Atoi(strings.TrimSpace(r))
		if err != nil || p <= 0 {
			return nil, fmt.Errorf("%s: invalid pid %q", flagName, r)
		}
		pids = append(pids, p)
	}
	return pids, nil
}
