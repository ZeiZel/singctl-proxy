package sysproxy

// State reports one macOS network service's observed proxy configuration, as
// read back via `networksetup -getwebproxy`/`-getsecurewebproxy`/
// `-getautoproxyurl`/`-getproxybypassdomains`.
type State struct {
	AutoProxyEnabled bool
	AutoProxyURL     string
	WebProxyEnabled  bool
	// SecureWebProxyEnabled reports the manual HTTPS proxy state
	// (`-getsecurewebproxy`) — previously never read, so a stray manual HTTPS
	// proxy left on (e.g. by another tool, or a half-applied prior state) was
	// invisible even though DisableWebProxy always turns it off alongside the
	// plain HTTP one.
	SecureWebProxyEnabled bool
	// BypassDomains is the current `-setproxybypassdomains` list
	// (`-getproxybypassdomains`), empty when unset/"Empty".
	BypassDomains []string
}

// NetworkSetup is singctl's port onto `networksetup(8)` — the only place this
// package (via the real darwin adapter, networksetup_darwin.go) shells out.
// Everything else in this package — PAC generation, Config, Manager's
// decision-making — is exercised in tests against a fake implementation, with
// no `networksetup` binary, no macOS, and no root required.
type NetworkSetup interface {
	// SetAutoProxyURL points service's Automatic Proxy Configuration at url
	// and turns it on. Implementations also turn OFF the manual web/secure
	// web proxy state first (mirrors `make proxy-on`/`proxy-pac`: PAC and the
	// manual proxy fields must not overlap), though Manager.Apply calls
	// DisableWebProxy explicitly too, so this is belt-and-suspenders.
	SetAutoProxyURL(service, url string) error
	// DisableAutoProxy turns off service's Automatic Proxy Configuration.
	DisableAutoProxy(service string) error
	// DisableWebProxy turns off service's manual web AND secure web proxy.
	DisableWebProxy(service string) error
	// SetBypassDomains sets service's Bypass Proxy Settings for These Hosts &
	// Domains list (`-setproxybypassdomains`) — a system-level mechanism
	// separate from (and belt-and-suspenders on top of) the PAC's own DIRECT
	// rules: some macOS clients don't consult the PAC's DIRECT return value
	// for every connection, but every client still honors this list. An
	// empty/nil domains clears it (mirrors `-setproxybypassdomains <service>
	// Empty`).
	SetBypassDomains(service string, domains []string) error
	// Status reads back service's current proxy configuration.
	Status(service string) (State, error)
	// Services lists the macOS network services `networksetup` knows about
	// (`networksetup -listallnetworkservices`).
	Services() ([]string, error)
}
