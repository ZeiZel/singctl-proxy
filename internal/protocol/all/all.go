// Package all is the composition root for singctl's protocol registry: the
// one place that lists every protocol module singctl ships and wires them
// into a *protocol.Registry. See docs/protocol-modules.md.
//
// There is deliberately no init()-time registration anywhere in the protocol
// packages: adding a protocol means adding one entry to the Registry
// constructor below, so the set of supported protocols is a property of this
// file rather than of which packages happened to be linked in.
package all

import (
	"singctl/internal/protocol"
	"singctl/internal/protocol/anytls"
	"singctl/internal/protocol/hysteria"
	"singctl/internal/protocol/hysteria2"
	"singctl/internal/protocol/shadowsocks"
	"singctl/internal/protocol/trojan"
	"singctl/internal/protocol/tuic"
	"singctl/internal/protocol/vless"
	"singctl/internal/protocol/vmess"
	"singctl/internal/protocol/wireguard"
)

// Registry builds the default protocol registry from every protocol module
// singctl ships. Callers build it once at their own composition root (main,
// or a test) and inject it downward — see docs/protocol-modules.md's DI rule.
//
// It panics on error: NewRegistry only fails on a wiring mistake (a duplicate
// scheme or module name), which must stop the program at startup rather than
// silently run with fewer protocols than intended.
func Registry() *protocol.Registry {
	r, err := protocol.NewRegistry(
		vless.New(),
		vmess.New(),
		trojan.New(),
		shadowsocks.New(),
		hysteria.New(),
		hysteria2.New(),
		tuic.New(),
		anytls.New(),
		wireguard.New(),
	)
	if err != nil {
		panic("protocol/all: " + err.Error())
	}
	return r
}
