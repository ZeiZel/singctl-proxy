package license

import "singctl/internal/feature"

// FeatureDescriptor documents the license CLI surface for --help / the man page.
// internal/feature is pure, so importing it here introduces no cycle.
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "license",
		Title:   "License",
		Summary: "activation and status",
		Doc: "singctl requires an active license (offline check against an embedded key). " +
			"Get a token from your provider, install it with `--license <token|file> --email <address>`, " +
			"check `--license-status`. Activation binds the license to this device; " +
			"re-activating on another device moves the binding there (last one wins). " +
			"A `make build-unlicensed` build disables the check.",
		Flags: []feature.FlagSpec{
			{Names: []string{"license", "install"}, Placeholder: "<token|path>",
				Usage: "install a license (token or path to a file) and exit",
				Env:   []string{"SINGCTL_LICENSE"}},
			{Names: []string{"license-status"},
				Usage: "show license status and exit"},
			{Names: []string{"json"},
				Usage: "with --license-status, print machine-readable JSON instead of text"},
			{Names: []string{"license-remove"},
				Usage: "remove the installed license and exit"},
			{Names: []string{"email"}, Placeholder: "<address>",
				Usage: "email for device activation registration (used with --license/--install)"},
		},
	}
}
