//go:build !darwin

package main

// stopSystemDaemon is a no-op off macOS: there is no LaunchDaemon to stop (Linux
// uses a plain binary in $PREFIX/bin; a systemd unit is a future follow-up).
func stopSystemDaemon() (bool, string) { return false, "" }
