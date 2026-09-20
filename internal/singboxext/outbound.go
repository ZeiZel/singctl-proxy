//go:build singbox

// Package singboxext adds singctl's own protocol support to the embedded
// sing-box core. sing-box's outbound registry is open — anything registered in
// it becomes a first-class outbound "type" in the config — so a transport
// upstream does not implement can be shipped here without forking sing-box.
//
// Today it contributes exactly one type, "vless-xhttp": VLESS carried over
// Xray's XHTTP transport (internal/xhttp), which upstream sing-box has no
// equivalent for. internal/singbox emits that type for any key with
// `type=xhttp`; everything else keeps using stock sing-box outbounds.
//
// Like internal/core/real.go this file is behind the `singbox` build tag, so
// the hermetic unit build never pulls the sing-box library.
package singboxext

import (
	"context"
	"encoding/json"
	"net"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-vmess/vless"
	"github.com/sagernet/sing/common"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"singctl/internal/xhttp"
)

// TypeVLESSXHTTP is the outbound "type" string in the generated config. It must
// stay in sync with xhttpOutboundType in internal/singbox/generate.go.
const TypeVLESSXHTTP = "vless-xhttp"

// XHTTPOutboundOptions mirrors sing-box's VLESSOutboundOptions minus the knobs
// XHTTP cannot carry (flow — XTLS Vision is raw-TCP only; transport — XHTTP is
// the transport; multiplex — XHTTP already multiplexes over HTTP), plus the
// xhttp settings block.
type XHTTPOutboundOptions struct {
	option.DialerOptions
	option.ServerOptions
	UUID    string             `json:"uuid"`
	Network option.NetworkList `json:"network,omitempty"`
	option.OutboundTLSOptionsContainer
	XHTTP XHTTPTransportOptions `json:"xhttp,omitempty"`
}

// XHTTPTransportOptions is the `xhttp` block of the outbound. Extra is the
// share link's `extra=` blob verbatim — Xray's full xhttpSettings object.
type XHTTPTransportOptions struct {
	Path  string          `json:"path,omitempty"`
	Host  string          `json:"host,omitempty"`
	Mode  string          `json:"mode,omitempty"`
	Extra json.RawMessage `json:"extra,omitempty"`
}

// RegisterOutbound adds the "vless-xhttp" type to a sing-box outbound registry.
func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[XHTTPOutboundOptions](registry, TypeVLESSXHTTP, NewXHTTPOutbound)
}

// XHTTPOutbound is a VLESS outbound whose stream runs over XHTTP. It follows
// sing-box's own vless outbound closely; only the way the underlying net.Conn
// is obtained differs.
type XHTTPOutbound struct {
	outbound.Adapter
	logger     logger.ContextLogger
	client     *vless.Client
	transport  *xhttp.Dialer
	serverAddr M.Socksaddr
}

func NewXHTTPOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options XHTTPOutboundOptions) (adapter.Outbound, error) {
	outboundDialer, err := dialer.New(ctx, options.DialerOptions, options.ServerIsDomain())
	if err != nil {
		return nil, err
	}
	serverAddr := options.ServerOptions.Build()

	tlsOptions := options.TLS
	httpVersion := decideHTTPVersion(tlsOptions)

	var tlsConfig tls.Config
	if tlsOptions != nil && tlsOptions.Enabled {
		tlsConfig, err = tls.NewClient(ctx, logger, options.Server, common.PtrValueOrDefault(tlsOptions))
		if err != nil {
			return nil, E.Cause(err, "create TLS client")
		}
		// XHTTP picks its HTTP version from the ALPN, so the handshake has to
		// actually offer it. A key that names its own ALPN keeps it.
		if len(tlsOptions.ALPN) == 0 {
			if httpVersion == xhttp.HTTPVersion2 {
				tlsConfig.SetNextProtos([]string{"h2"})
			} else {
				tlsConfig.SetNextProtos([]string{"http/1.1"})
			}
		}
	}

	settings, err := xhttp.ParseSettings(string(options.XHTTP.Extra))
	if err != nil {
		return nil, err
	}
	// The link's own parameters win over the same fields inside `extra`.
	if options.XHTTP.Path != "" {
		settings.Path = options.XHTTP.Path
	}
	if options.XHTTP.Host != "" {
		settings.Host = options.XHTTP.Host
	}
	if options.XHTTP.Mode != "" {
		settings.Mode = options.XHTTP.Mode
	}

	scheme := "http"
	if tlsConfig != nil {
		scheme = "https"
	}
	// Host priority matches Xray: the explicit host, then the SNI, then the
	// server address. This is the Host header the origin matches on, and it is
	// deliberately independent of where we actually connect.
	authority := settings.Host
	if authority == "" && tlsOptions != nil {
		authority = tlsOptions.ServerName
	}
	if authority == "" {
		authority = options.Server
	}

	transport, err := xhttp.New(xhttp.Config{
		Dial: func(ctx context.Context) (net.Conn, error) {
			conn, err := outboundDialer.DialContext(ctx, N.NetworkTCP, serverAddr)
			if err != nil {
				return nil, err
			}
			if tlsConfig == nil {
				return conn, nil
			}
			tlsConn, err := tls.ClientHandshake(ctx, conn, tlsConfig)
			if err != nil {
				conn.Close()
				return nil, err
			}
			return tlsConn, nil
		},
		Scheme:      scheme,
		Authority:   authority,
		HTTPVersion: httpVersion,
		Reality:     realityEnabled(tlsOptions),
		Settings:    settings,
	})
	if err != nil {
		return nil, err
	}

	// Flow is intentionally empty: XTLS Vision needs a raw TLS stream to splice
	// and is rejected by Xray itself on non-raw transports.
	client, err := vless.NewClient(options.UUID, "", logger)
	if err != nil {
		return nil, err
	}

	logger.Debug("xhttp outbound ", tag, ": mode ", transport.Mode(), ", http ", httpVersion, ", host ", authority)

	return &XHTTPOutbound{
		Adapter:    outbound.NewAdapterWithDialerOptions(TypeVLESSXHTTP, tag, options.Network.Build(), options.DialerOptions),
		logger:     logger,
		client:     client,
		transport:  transport,
		serverAddr: serverAddr,
	}, nil
}

// decideHTTPVersion mirrors Xray's rule: REALITY always speaks HTTP/2, a plain
// connection HTTP/1.1, and otherwise the ALPN decides (a single "http/1.1" or
// "h3" entry picks that version, anything else means HTTP/2).
// realityEnabled reports whether this profile runs over REALITY.
func realityEnabled(tlsOptions *option.OutboundTLSOptions) bool {
	return tlsOptions != nil && tlsOptions.Reality != nil && tlsOptions.Reality.Enabled
}

func decideHTTPVersion(tlsOptions *option.OutboundTLSOptions) string {
	if tlsOptions == nil || !tlsOptions.Enabled {
		return xhttp.HTTPVersion11
	}
	if realityEnabled(tlsOptions) {
		return xhttp.HTTPVersion2
	}
	if len(tlsOptions.ALPN) != 1 {
		return xhttp.HTTPVersion2
	}
	switch tlsOptions.ALPN[0] {
	case "http/1.1":
		return xhttp.HTTPVersion11
	case "h3":
		return xhttp.HTTPVersion3
	}
	return xhttp.HTTPVersion2
}

func (h *XHTTPOutbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination

	conn, err := h.transport.DialContext(ctx)
	if err != nil {
		return nil, err
	}
	switch N.NetworkName(network) {
	case N.NetworkTCP:
		h.logger.InfoContext(ctx, "outbound connection to ", destination)
		return h.client.DialEarlyConn(conn, destination)
	case N.NetworkUDP:
		h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
		// xudp is sing-box's default packet encoding for VLESS, and the only
		// one Xray's XHTTP servers are configured for in practice.
		return h.client.DialEarlyXUDPPacketConn(conn, destination)
	default:
		conn.Close()
		return nil, E.Extend(N.ErrUnknownNetwork, network)
	}
}

func (h *XHTTPOutbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	ctx, metadata := adapter.ExtendContext(ctx)
	metadata.Outbound = h.Tag()
	metadata.Destination = destination

	h.logger.InfoContext(ctx, "outbound packet connection to ", destination)
	conn, err := h.transport.DialContext(ctx)
	if err != nil {
		return nil, err
	}
	packetConn, err := h.client.DialEarlyXUDPPacketConn(conn, destination)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return packetConn, nil
}

// InterfaceUpdated drops pooled HTTP connections after a network change, so the
// next dial re-resolves and re-binds instead of writing into a dead socket.
func (h *XHTTPOutbound) InterfaceUpdated() {
	h.transport.Close()
}

func (h *XHTTPOutbound) Close() error {
	return h.transport.Close()
}
