//go:build darwin

package runtime

import (
	"context"
	"os/exec"
	"time"

	"singctl/internal/netstate"
)

// OSRouteController cleans up orphan forwarder TUN devices left by a crashed
// previous run. It destroys ONLY devices carrying our 198.18.0.x address — never
// Cisco. Requires root; best-effort (never blocks startup on failure).
type OSRouteController struct{}

func NewOSRouteController() OSRouteController { return OSRouteController{} }

func (OSRouteController) CleanupOrphans() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/sbin/ifconfig").Output()
	if err != nil {
		return nil
	}
	// Destroy our leaked TUN devices AND delete their override routes — but only
	// when such a device is actually present (cleanupCommands returns nil otherwise).
	for _, argv := range cleanupCommands(netstate.ParseIfconfig(out), netstate.OurTunAddrPrefix) {
		_ = exec.CommandContext(ctx, argv[0], argv[1:]...).Run()
	}
	return nil
}
