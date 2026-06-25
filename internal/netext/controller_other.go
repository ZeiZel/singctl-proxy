//go:build !darwin

package netext

// New returns a no-op controller off macOS: there is no system extension, so
// per-app capture is unavailable and singctl uses the procproxy env/flag path.
// Keeps the composition root platform-agnostic (build the controller
// unconditionally; it simply reports Available()==false here).
func New(socksHost string, socksPort int) Controller { return noopController{} }

type noopController struct{}

func (noopController) Available() bool           { return false }
func (noopController) AddTarget(string) error    { return nil }
func (noopController) RemoveTarget(string) error { return nil }
func (noopController) Targets() []string         { return nil }

// BundleID / BundleIDForPID are macOS concepts; off darwin they are unresolved.
func BundleID(string) string    { return "" }
func BundleIDForPID(int) string { return "" }
