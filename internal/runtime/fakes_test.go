package runtime

import "context"

// In-memory fakes for the manager's injected dependencies.

// blockingCore is a core.Core whose Start never returns until stop is closed
// (or never, if stop is nil) — it stands in for a wedged sing-box forwarder
// (F2 item 4: "start forwarder: no physical interface detected" is the
// reported failure, but a hang is the harder case a timeout must also cover).
// ctx is deliberately ignored: the real sing-box core does the same (see
// internal/core/real.go's boxCore.Start), so a manager-level timeout — not
// context cancellation — is the only thing that can bound this.
type blockingCore struct {
	stop     chan struct{}
	closeErr error
}

func (b *blockingCore) Start(ctx context.Context) error {
	if b.stop == nil {
		select {} // block forever
	}
	<-b.stop
	return nil
}

func (b *blockingCore) Close() error { return b.closeErr }

type fakeBuilder struct {
	proxyErr   error
	fwdErr     error
	proxyCalls []string // physIface arg of each ProxyConfig call, in order
	fwdCalls   int
}

func (b *fakeBuilder) ProxyConfig(physIface string) ([]byte, error) {
	b.proxyCalls = append(b.proxyCalls, physIface)
	if b.proxyErr != nil {
		return nil, b.proxyErr
	}
	return []byte(`{"proxy":"` + physIface + `"}`), nil
}

func (b *fakeBuilder) ForwarderConfig() ([]byte, error) {
	b.fwdCalls++
	if b.fwdErr != nil {
		return nil, b.fwdErr
	}
	return []byte(`{"fwd":1}`), nil
}

type fakeProber struct {
	iface string
	err   error
}

func (p *fakeProber) PhysicalDefault() (string, error) { return p.iface, p.err }

type fakeRoutes struct {
	cleanupCalls int
	cleanupErr   error
}

func (r *fakeRoutes) CleanupOrphans() error {
	r.cleanupCalls++
	return r.cleanupErr
}
