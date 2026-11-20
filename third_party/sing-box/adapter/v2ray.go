package adapter

import (
	"context"
	"net"

	N "github.com/sagernet/sing/common/network"
)

type V2RayServerTransport interface {
	Network() []string
	Serve(listener net.Listener) error
	ServePacket(listener net.PacketConn) error
	Close() error
}

type V2RayServerTransportHandler interface {
	N.TCPConnectionHandlerEx
}

type V2RayClientTransport interface {
	DialContext(ctx context.Context) (net.Conn, error)
	Close() error
}

// ResetV2RayClientTransport releases pooled connections for a network change.
// Some transports distinguish a reconnect from permanently closing the client.
func ResetV2RayClientTransport(transport V2RayClientTransport) {
	if listener, ok := transport.(InterfaceUpdateListener); ok {
		listener.InterfaceUpdated()
	} else if transport != nil {
		_ = transport.Close()
	}
}
