package v2raygrpclite

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
)

type testDialer struct{}

func (testDialer) DialContext(ctx context.Context, network string, dst M.Socksaddr) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, dst.String())
}
func (testDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, context.Canceled
}

func TestRoundTripFailureUnblocksWrite(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			time.Sleep(20 * time.Millisecond)
			_ = conn.Close()
		}
	}()
	client := NewClient(context.Background(), testDialer{}, M.ParseSocksaddr(listener.Addr().String()), option.V2RayGRPCOptions{}, nil).(*Client)
	conn, err := client.DialContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(100 * time.Millisecond)
	writeDone := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("probe")); writeDone <- err }()
	select {
	case err := <-writeDone:
		if err == nil {
			t.Fatal("Write unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("Write remained blocked after RoundTrip failure")
	}
}
