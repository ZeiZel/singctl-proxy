package procproxy

import "singctl/internal/feature"

// FeatureDescriptor describes per-process proxying for the CLI.
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "procproxy",
		Title:   "Process proxying",
		Summary: "by PID / launch / restart",
		Doc: "On Linux — real per-process traffic capture by PID (cgroup v2 + " +
			"nftables). On other OSes — launch a command with proxy-env forwarded. " +
			"--restart-pid restarts an already-running process in proxy mode.",
		Flags: []feature.FlagSpec{
			{Names: []string{"route-pid"}, Placeholder: "<pid>", Repeatable: true,
				Usage: "route a process by PID through the proxy (Linux)"},
			{Names: []string{"restart-pid"}, Placeholder: "<pid>", Repeatable: true,
				Usage: "restart a process in proxy mode"},
			{Names: []string{"launch"}, Placeholder: "-- <cmd>",
				Usage: "launch a command through the proxy"},
		},
	}
}
