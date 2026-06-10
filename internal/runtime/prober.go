package runtime

import (
	"context"
	"time"

	"singctl/internal/netstate"
)

// NetProber implements InterfaceProber by reusing the passive netstate detector.
// It returns the physical default interface, which (by construction of
// pickPhysical) is a non-tunnel interface with an IP gateway — so it ignores
// Cisco's global primary even when Cisco owns the unscoped default route.
type NetProber struct {
	det     *netstate.Detector
	timeout time.Duration
}

func NewNetProber(det *netstate.Detector) *NetProber {
	return &NetProber{det: det, timeout: 3 * time.Second}
}

func (p *NetProber) PhysicalDefault() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()
	ns, err := p.det.Observe(ctx)
	if err != nil {
		return "", err
	}
	return ns.PhysicalIface, nil
}
