//go:build !darwin

package runtime

// OSRouteController is a no-op on non-darwin platforms: orphan-TUN cleanup is
// a macOS-specific recovery path (utun devices surviving a crashed run);
// sing-box removes its own TUN/routes on these platforms.
type OSRouteController struct{}

func NewOSRouteController() OSRouteController { return OSRouteController{} }

func (OSRouteController) CleanupOrphans() error { return nil }
