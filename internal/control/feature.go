package control

import "singctl/internal/feature"

// FeatureDescriptor describes the instance attach/control feature for the CLI.
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "control",
		Title:   "Instance control",
		Summary: "attach / stop / status",
		Doc: "A running instance publishes instance.json and listens on a Unix socket. " +
			"These commands work without root: attach to the logs of an instance " +
			"already running in another tab, check its status, or stop it.",
		Flags: []feature.FlagSpec{
			{Names: []string{"attach"}, Usage: "attach to the running instance's logs (ctrl+c to detach)"},
			{Names: []string{"stop"}, Usage: "stop the running instance"},
			{Names: []string{"status"}, Usage: "show the running instance's status"},
		},
	}
}
