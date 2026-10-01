package v2raygrpc

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	M "github.com/sagernet/sing/common/metadata"
)

type blockingDialer struct{ calls atomic.Int32 }

func (d *blockingDialer) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	d.calls.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}
func (*blockingDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, context.Canceled
}

func TestClientCloseCancelsBlockedDial(t *testing.T) {
	d := new(blockingDialer)
	transport, err := NewClient(context.Background(), d, M.ParseSocksaddr("127.0.0.1:443"), option.V2RayGRPCOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := transport.(*Client)
	result := make(chan error, 1)
	go func() { _, err := c.DialContext(context.Background()); result <- err }()
	deadline := time.After(time.Second)
	for d.calls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("dialer was not called")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel dial")
	}
	before := d.calls.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = c.DialContext(ctx)
	if err == nil {
		t.Fatal("closed client dial unexpectedly succeeded")
	}
	if got := d.calls.Load(); got != before {
		t.Fatalf("closed client started dial: %d -> %d", before, got)
	}
}
