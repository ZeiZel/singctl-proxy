// Package wireguard is the first endpoint-kind, config-input protocol module:
// WireGuard arrives as an INI file rather than a share link, and sing-box
// models it as an endpoint rather than an outbound. See
// docs/protocol-modules.md "Phase 1", item 4.
package wireguard

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

// name is the protocol.Name every Profile produced here carries.
const name protocol.Name = "wireguard"

// Params is wireguard's parsed configuration: Parse produces it, RenderNode
// consumes it. It mirrors the shape of a WireGuard [Interface]/[Peer] INI
// file rather than the sing-box endpoint it becomes — RenderNode owns the
// engine-specific transformation (e.g. defaulting AllowedIPs).
type Params struct {
	PrivateKey string
	// Address is the interface's own prefixes, comma-separated in the source
	// config (e.g. "10.0.0.2/32, fd00::2/128").
	Address    []string
	MTU        int
	ListenPort int
	// DNS is parsed for completeness but deliberately never rendered: singctl
	// owns its own DNS bootstrap and routing (see internal/singbox), and a
	// config's DNS servers would fight that rather than complement it.
	DNS   []string
	Peers []Peer
}

// Peer is one [Peer] section.
type Peer struct {
	PublicKey    string
	PresharedKey string
	// EndpointHost/EndpointPort come from splitting "Endpoint = host:port".
	// Both are empty when the section has no Endpoint= (a peer that only
	// receives connections, never dials out).
	EndpointHost        string
	EndpointPort        int
	AllowedIPs          []string
	PersistentKeepalive int
}

// Module is the wireguard protocol adapter: InputConfig, KindEndpoint, no
// schemes. It does not implement protocol.Relabeler — an INI file has
// nowhere to put a display name, so the registry reports that cleanly rather
// than this module silently doing nothing.
type Module struct{}

// New builds a wireguard Module.
func New() *Module { return &Module{} }

// Descriptor implements protocol.Module.
func (*Module) Descriptor() protocol.Descriptor {
	return protocol.Descriptor{
		Name:  name,
		Title: "WireGuard",
		Kind:  protocol.KindEndpoint,
		Input: protocol.InputConfig,
	}
}

// Sniff implements protocol.Sniffer. It is deliberately conservative: it only
// recognises input that contains a bare "[Interface]" section header (case-
// insensitive, WireGuard keys generally are), never arbitrary text — a false
// positive here would turn a helpful "unsupported input" message from the
// registry into a confusing parse error from this module instead.
func (*Module) Sniff(raw string) bool {
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		if strings.ToLower(strings.TrimSpace(sc.Text())) == "[interface]" {
			return true
		}
	}
	return false
}

// Parse parses a standard WireGuard INI config into a Profile carrying
// Params. Label is derived from the first peer's endpoint host: a WireGuard
// config carries no name field of its own, and the peer we actually dial is
// the most honest stable label available.
func (*Module) Parse(raw string) (protocol.Profile, error) {
	params, err := parseINI(raw)
	if err != nil {
		return protocol.Profile{}, err
	}
	label := ""
	if len(params.Peers) > 0 {
		label = params.Peers[0].EndpointHost
	}
	return protocol.Profile{
		Protocol: name,
		Label:    label,
		Raw:      raw,
		Params:   params,
	}, nil
}

// RenderNode implements singbox.Renderer, building the sing-box wireguard
// endpoint for this profile.
func (*Module) RenderNode(p protocol.Profile, o singbox.RenderOpts) (any, error) {
	params, ok := p.Params.(Params)
	if !ok {
		return nil, fmt.Errorf("wireguard: RenderNode got Params of type %T, want wireguard.Params", p.Params)
	}

	peers := make([]singbox.WireGuardPeer, len(params.Peers))
	for i, peer := range params.Peers {
		allowed := peer.AllowedIPs
		if len(allowed) == 0 {
			// wg-quick defaults an omitted AllowedIPs to a full tunnel;
			// sing-box does not do this itself, so we must — an operator who
			// hands over a key with no AllowedIPs means "route everything".
			allowed = []string{"0.0.0.0/0", "::/0"}
		}
		peers[i] = singbox.WireGuardPeer{
			Address:                     peer.EndpointHost,
			Port:                        peer.EndpointPort,
			PublicKey:                   peer.PublicKey,
			PreSharedKey:                peer.PresharedKey,
			AllowedIPs:                  allowed,
			PersistentKeepaliveInterval: peer.PersistentKeepalive,
		}
	}

	return &singbox.WireGuardEndpoint{
		Type:           "wireguard",
		Tag:            o.Tag,
		MTU:            params.MTU,
		Address:        params.Address,
		PrivateKey:     params.PrivateKey,
		ListenPort:     params.ListenPort,
		Peers:          peers,
		BindInterface:  o.BindInterface,
		ConnectTimeout: o.ConnectTimeout,
	}, nil
}

// parseINI parses a WireGuard config. Keys are case-insensitive and may be
// spaced around "="; comments start with "#" or ";" (whole-line only, as
// wg-quick itself accepts). Required fields are rejected by name and section
// so a bad config fails with an actionable message rather than a zero-value
// endpoint.
func parseINI(raw string) (Params, error) {
	var params Params
	var haveInterface bool
	var cur *Peer // non-nil while inside a [Peer] section
	section := ""

	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			switch strings.ToLower(strings.TrimSpace(line[1 : len(line)-1])) {
			case "interface":
				if haveInterface {
					return Params{}, errors.New("wireguard: config has more than one [Interface] section")
				}
				section = "interface"
				haveInterface = true
			case "peer":
				section = "peer"
				params.Peers = append(params.Peers, Peer{})
				cur = &params.Peers[len(params.Peers)-1]
			default:
				// An unknown section (or a case-mangled one) is ignored rather
				// than misattributed: its keys are simply skipped below.
				section = ""
				cur = nil
			}
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue // not a "key = value" line; ignore rather than fail hard
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		var err error
		switch section {
		case "interface":
			err = setInterfaceField(&params, key, value)
		case "peer":
			if cur != nil {
				err = setPeerField(cur, key, value)
			}
		}
		if err != nil {
			return Params{}, err
		}
	}
	if err := sc.Err(); err != nil {
		return Params{}, fmt.Errorf("wireguard: %w", err)
	}

	if !haveInterface {
		return Params{}, errors.New("wireguard: config has no [Interface] section")
	}
	if params.PrivateKey == "" {
		return Params{}, errors.New("wireguard: [Interface]: PrivateKey is required")
	}
	if len(params.Address) == 0 {
		return Params{}, errors.New("wireguard: [Interface]: Address is required")
	}
	if len(params.Peers) == 0 {
		return Params{}, errors.New("wireguard: config has no [Peer] section")
	}
	for i, peer := range params.Peers {
		if peer.PublicKey == "" {
			return Params{}, fmt.Errorf("wireguard: [Peer] #%d: PublicKey is required", i+1)
		}
	}
	return params, nil
}

func setInterfaceField(params *Params, key, value string) error {
	switch key {
	case "privatekey":
		params.PrivateKey = value
	case "address":
		params.Address = splitCSV(value)
	case "mtu":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("wireguard: [Interface]: MTU: %w", err)
		}
		params.MTU = n
	case "listenport":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("wireguard: [Interface]: ListenPort: %w", err)
		}
		params.ListenPort = n
	case "dns":
		// Parsed but never rendered — see the Params.DNS doc comment.
		params.DNS = splitCSV(value)
	}
	return nil
}

func setPeerField(peer *Peer, key, value string) error {
	switch key {
	case "publickey":
		peer.PublicKey = value
	case "presharedkey":
		peer.PresharedKey = value
	case "endpoint":
		host, port, err := splitEndpoint(value)
		if err != nil {
			return fmt.Errorf("wireguard: [Peer]: Endpoint: %w", err)
		}
		peer.EndpointHost = host
		peer.EndpointPort = port
	case "allowedips":
		peer.AllowedIPs = splitCSV(value)
	case "persistentkeepalive":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("wireguard: [Peer]: PersistentKeepalive: %w", err)
		}
		peer.PersistentKeepalive = n
	}
	return nil
}

// splitCSV splits a comma-separated list (addresses, CIDRs, DNS servers),
// trimming whitespace around each element and dropping empties.
func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// splitEndpoint splits "host:port", including IPv6 written in brackets
// ("[2001:db8::1]:51820"); net.SplitHostPort already strips the brackets.
func splitEndpoint(v string) (host string, port int, err error) {
	host, portStr, err := net.SplitHostPort(v)
	if err != nil {
		return "", 0, err
	}
	p, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	return host, p, nil
}
