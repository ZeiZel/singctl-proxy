package runtime

import (
	"singctl/internal/singbox"
	"singctl/internal/vless"
)

// ProfileConfigBuilder builds the two sing-box configs from one or more parsed
// VLESS profiles. It implements ConfigBuilder. LogPath, if set, redirects
// sing-box logs to a file so they don't corrupt the TUI. Ports overrides the
// local listen ports; its zero value means the defaults (socks 1080, http 2080).
// ClashAPI, if set, enables sing-box's Clash API (connection observability +
// per-server latency). URLTest tunes the multi-server failover group.
type ProfileConfigBuilder struct {
	Profiles vless.ProfileSet
	LogPath  string
	Ports    singbox.Ports
	ClashAPI *singbox.ClashAPI
	URLTest  singbox.URLTestParams
}

func (b ProfileConfigBuilder) ProxyConfig(physIface string) ([]byte, error) {
	cfg, err := singbox.GenerateProxyConfigOpts(b.Profiles, singbox.ProxyOpts{
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
	cfg, err := singbox.GenerateForwarderConfigSet(b.Profiles, b.Ports)
	if err != nil {
		return nil, err
	}
	b.applyLog(&cfg)
	return singbox.MarshalIndented(cfg)
}

func (b ProfileConfigBuilder) applyLog(cfg *singbox.Config) {
	if b.LogPath != "" && cfg.Log != nil {
		cfg.Log.Output = b.LogPath
	}
}
