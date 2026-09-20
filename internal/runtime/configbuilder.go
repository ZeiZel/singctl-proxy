package runtime

import (
	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

// ProfileConfigBuilder builds the two sing-box configs from one or more parsed
// parsed profiles. It implements ConfigBuilder. Registry is the protocol
// registry every profile is rendered through — injected, not a package-level
// singleton (see docs/protocol-modules.md). LogPath, if set, redirects
// sing-box logs to a file so they don't corrupt the TUI. Ports overrides the
// local listen ports; its zero value means the defaults (socks 1080, http 2080).
// ClashAPI, if set, enables sing-box's Clash API (connection observability +
// per-server latency). URLTest tunes the multi-server failover group.
type ProfileConfigBuilder struct {
	Registry *protocol.Registry
	Profiles []protocol.Profile
	LogPath  string
	LogLevel string // sing-box log level; "" means "warn" (quiet — avoids the per-connection info firehose)
	Ports    singbox.Ports
	ClashAPI *singbox.ClashAPI
	URLTest  singbox.URLTestParams
}

func (b ProfileConfigBuilder) ProxyConfig(physIface string) ([]byte, error) {
	cfg, err := singbox.GenerateProxyConfigOpts(b.Registry, b.Profiles, singbox.ProxyOpts{
		PhysIface: physIface,
		Ports:     b.Ports,
		ClashAPI:  b.ClashAPI,
		URLTest:   b.URLTest,
	})
	if err != nil {
		return nil, err
	}
	b.applyLog(&cfg)
	return singbox.MarshalIndented(cfg)
}

func (b ProfileConfigBuilder) ForwarderConfig() ([]byte, error) {
	cfg, err := singbox.GenerateForwarderConfigSet(b.Registry, b.Profiles, b.Ports)
	if err != nil {
		return nil, err
	}
	b.applyLog(&cfg)
	return singbox.MarshalIndented(cfg)
}

func (b ProfileConfigBuilder) applyLog(cfg *singbox.Config) {
	if cfg.Log == nil {
		return
	}
	if b.LogPath != "" {
		cfg.Log.Output = b.LogPath
	}
	// Quiet by default: the generated configs default to "info", which logs
	// every connection and grew the log to hundreds of MB. "warn" keeps errors
	// (what we actually debug with) while dropping the per-connection firehose.
	level := b.LogLevel
	if level == "" {
		level = "warn"
	}
	cfg.Log.Level = level
}
