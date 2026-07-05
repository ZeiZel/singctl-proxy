package runtime

import "singctl/internal/feature"

// FeatureDescriptor describes the proxy/VPN mode + run options for the CLI.
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "proxy",
		Title:   "Mode and startup",
		Summary: "proxy / VPN / ports",
		Doc: "Controls the run mode: local proxy (SOCKS+HTTP) or " +
			"system VPN (TUN). There is no interactive terminal UI: --headless " +
			"runs in the foreground, --daemon detaches to the background.",
		Flags: []feature.FlagSpec{
			{Names: []string{"p", "proxy"}, Usage: "enable proxy mode at startup"},
			{Names: []string{"vpn"}, Usage: "enable VPN (TUN) mode at startup"},
			{Names: []string{"port"}, Placeholder: "<n>", Default: "1080/2080",
				Usage: "local SOCKS port (HTTP listens on port+1)", Env: []string{"SINGCTL_PORT"}},
			{Names: []string{"headless"}, Usage: "run in the foreground (required to actually run the proxy)"},
			{Names: []string{"l", "logs"}, Usage: "stream logs to stdout"},
			{Names: []string{"verbose"}, Usage: "verbose sing-box logs (level=info; quiet warn by default)"},
			{Names: []string{"daemon"}, Usage: "run detached in the background and exit (manage via --status/--stop)"},
		},
	}
}
