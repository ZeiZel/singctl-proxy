package v2raygrpc

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

var _ adapter.V2RayClientTransport = (*Client)(nil)

type Client struct {
	ctx         context.Context
	cancel      context.CancelFunc
	dialer      N.Dialer
	serverAddr  string
	serviceName string
	dialOptions []grpc.DialOption
	conn        atomic.Pointer[grpc.ClientConn]
	connAccess  sync.Mutex
	closed      atomic.Bool
}

func NewClient(ctx context.Context, dialer N.Dialer, serverAddr M.Socksaddr, options option.V2RayGRPCOptions, tlsConfig tls.Config) (adapter.V2RayClientTransport, error) {
	var dialOptions []grpc.DialOption
	if tlsConfig != nil {
		if len(tlsConfig.NextProtos()) == 0 {
			tlsConfig.SetNextProtos([]string{http2.NextProtoTLS})
		}
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(NewTLSTransportCredentials(tlsConfig)))
	} else {
		dialOptions = append(dialOptions, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	if options.IdleTimeout > 0 {
		dialOptions = append(dialOptions, grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                time.Duration(options.IdleTimeout),
			Timeout:             time.Duration(options.PingTimeout),
			PermitWithoutStream: options.PermitWithoutStream,
		}))
	}
	dialOptions = append(dialOptions, grpc.WithConnectParams(grpc.ConnectParams{
		Backoff: backoff.Config{
			BaseDelay:  500 * time.Millisecond,
			Multiplier: 1.5,
			Jitter:     0.2,
			MaxDelay:   19 * time.Second,
		},
		MinConnectTimeout: 5 * time.Second,
	}))
	dialOptions = append(dialOptions, grpc.WithContextDialer(func(ctx context.Context, server string) (net.Conn, error) {
		return dialer.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr(server))
	}))
	//nolint:staticcheck
	dialOptions = append(dialOptions, grpc.WithReturnConnectionError(), grpc.FailOnNonTempDialError(true), grpc.WithBlock())
	clientCtx, cancel := context.WithCancel(ctx)
	return &Client{
		ctx:         clientCtx,
		cancel:      cancel,
		dialer:      dialer,
		serverAddr:  serverAddr.String(),
		serviceName: options.ServiceName,
		dialOptions: dialOptions,
	}, nil
}

func (c *Client) connect(ctx context.Context) (*grpc.ClientConn, error) {
	if c.closed.Load() {
		return nil, errors.New("grpc client is closed")
	}
	conn := c.conn.Load()
	if conn != nil && conn.GetState() != connectivity.Shutdown {
		return conn, nil
	}
	// Do not hold connAccess while dialing: the caller's probe context must be
	// able to cancel even when another connection attempt is in progress.
	dialCtx, stop := context.WithCancel(ctx)
	lifetimeStop := context.AfterFunc(c.ctx, stop)
	defer func() { lifetimeStop(); stop() }()
	conn, err := grpc.DialContext(dialCtx, c.serverAddr, c.dialOptions...)
	if err != nil {
		return nil, err
	}
	c.connAccess.Lock()
	defer c.connAccess.Unlock()
	if c.closed.Load() {
		_ = conn.Close()
		return nil, errors.New("grpc client is closed")
	}
	current := c.conn.Load()
	if current != nil && current.GetState() != connectivity.Shutdown {
		_ = conn.Close()
		return current, nil
	}
	c.conn.Store(conn)
	return conn, nil
}

func (c *Client) DialContext(ctx context.Context) (net.Conn, error) {
	clientConn, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	client := NewGunServiceClient(clientConn).(GunServiceCustomNameClient)
	ctx, cancel := context.WithCancelCause(ctx)
	stream, err := client.TunCustomName(ctx, c.serviceName)
	if err != nil {
		cancel(err)
		return nil, err
	}
	return NewGRPCConn(stream, cancel), nil
}

func (c *Client) Close() error {
	c.connAccess.Lock()
	defer c.connAccess.Unlock()
	c.closed.Store(true)
	c.cancel()
	conn := c.conn.Swap(nil)
	if conn != nil {
		return conn.Close()
	}
	return nil
}
