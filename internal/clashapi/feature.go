package clashapi

import "singctl/internal/feature"

// FeatureDescriptor describes the observability (Clash API) feature for the CLI.
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "observability",
		Title:   "Observability",
		Summary: "Clash API + server latencies",
		Doc: "The sing-box Clash API (loopback only, random secret) is polled " +
			"for live connections (source process, destination, chain) and server " +
			"latencies within the failover group.",
		Flags: []feature.FlagSpec{
			{Names: []string{"clash-api"}, Placeholder: "<host:port>", Default: "127.0.0.1:9090",
				Usage: "Clash API address (connection logs + latencies)", Env: []string{"SINGCTL_CLASH_API"}},
			{Names: []string{"no-clash-api"}, Usage: "disable the Clash API"},
			{Names: []string{"clash-secret"}, Placeholder: "<s>", Default: "random per run",
				Usage: "Clash API secret", Env: []string{"SINGCTL_CLASH_SECRET"}},
			{Names: []string{"urltest-url"}, Placeholder: "<url>", Default: "gstatic generate_204",
				Usage: "URL used to probe servers for failover"},
			{Names: []string{"urltest-interval"}, Placeholder: "<d>", Default: "3m",
				Usage: "server re-check interval"},
			{Names: []string{"urltest-tolerance"}, Placeholder: "<ms>", Default: "50",
				Usage: "server switch hysteresis, ms"},
		},
	}
}
