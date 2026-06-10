package runtime

// In-memory fakes for the manager's injected dependencies.

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
