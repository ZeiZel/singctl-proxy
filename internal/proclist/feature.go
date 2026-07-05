package proclist

import "singctl/internal/feature"

// FeatureDescriptor describes the process picker backing --route-pid/
// --restart-pid and the control socket's PROC-LIST (used by the GUI's process
// picker); it has no flags of its own.
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "processes",
		Title:   "Process list",
		Summary: "process picker for --route-pid / --restart-pid",
		Doc: "Lists processes with network sockets (PID, local ports, name), " +
			"making it easier to find the process to proxy. macOS uses lsof, " +
			"Linux uses /proc.",
	}
}
