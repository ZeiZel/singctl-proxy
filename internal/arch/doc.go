// Package arch holds architecture-enforcement tests that run as part of the
// normal unit suite: import-boundary checks (sing-box only inside internal/core,
// os/exec only inside OS adapters) and Cisco-isolation (no source ever
// references Cisco's install path). Keeping these as Go tests means a violation
// fails CI before any real run.
package arch
