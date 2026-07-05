package vless

import "singctl/internal/feature"

// FeatureDescriptor describes the connection-keys feature for the CLI registry.
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "keys",
		Title:   "Keys (VLESS)",
		Summary: "connection server(s)",
		Doc: "One or more vless:// keys. With multiple keys, sing-box " +
			"builds a urltest group and automatically picks the fastest available server " +
			"(input order sets priority).",
		Flags: []feature.FlagSpec{
			{Names: []string{"k", "key"}, Placeholder: "<vless://...>",
				Usage: "vless key; repeat for multiple servers (failover)",
				Env:   []string{"SINGCTL_KEY", "SINGCTL_KEYS"}, Repeatable: true},
			{Names: []string{"no-save"}, Usage: "do not save the key to ~/.config/singctl"},
		},
	}
}
