package runtime

import (
	"singctl/internal/singbox"
	"singctl/internal/vless"
)

// ProfileConfigBuilder builds the two sing-box configs from one parsed VLESS
// profile. It implements ConfigBuilder. LogPath, if set, redirects sing-box logs
// to a file so they don't corrupt the TUI.
type ProfileConfigBuilder struct {
	Profile vless.ServerProfile
	LogPath string
}

func (b ProfileConfigBuilder) ProxyConfig(physIface string) ([]byte, error) {
	cfg, err := singbox.GenerateProxyConfig(b.Profile, physIface)
	if err != nil {
		return nil, err
	}
	b.applyLog(&cfg)
	return singbox.MarshalIndented(cfg)
}

func (b ProfileConfigBuilder) ForwarderConfig() ([]byte, error) {
	cfg, err := singbox.GenerateForwarderConfig(b.Profile)
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
