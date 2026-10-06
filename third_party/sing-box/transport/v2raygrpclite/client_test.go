package v2raygrpclite

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
)

type testDialer struct{}

type blockingWriter struct {
	done    chan struct{}
	started chan struct{}
	err     error
}

func (w *blockingWriter) Write([]byte) (int, error) {
	select {
	case <-w.started:
	default:
		close(w.started)
	}
	<-w.done
	return 0, w.err
}
func (w *blockingWriter) Close() error {
	select {
	case <-w.done:
	default:
		close(w.done)
	}
	return nil
}

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

func TestWriteDeadlineAbortsBlackhole(t *testing.T) {
	writer := &blockingWriter{done: make(chan struct{}), started: make(chan struct{}), err: context.Canceled}
	conn := newLateGunConn(writer, writer, nil)
	writeDone := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("probe")); writeDone <- err }()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("write did not start")
	}
	if err := conn.SetWriteDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err == nil {
		t.Fatal("blackhole write unexpectedly succeeded")
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
}

func TestWriteBufferHonorsDeadline(t *testing.T) {
	writer := &blockingWriter{done: make(chan struct{}), started: make(chan struct{}), err: context.Canceled}
	conn := newLateGunConn(writer, writer, nil)
	if err := conn.SetWriteDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	b := buf.NewSize(64)
	b.Advance(7)
	_, _ = b.WriteString("probe")
	if err := conn.WriteBuffer(b); err == nil {
		t.Fatal("WriteBuffer unexpectedly succeeded")
	}
}

func TestWriteDeadlineExtendAndClear(t *testing.T) {
	writer := &blockingWriter{done: make(chan struct{}), started: make(chan struct{})}
	conn := newLateGunConn(writer, writer, nil)
	done := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("probe")); done <- err }()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("write did not start")
	}
	if err := conn.SetWriteDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("old deadline fired after extension: %v", err)
	default:
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(250 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("extended deadline fired after clear: %v", err)
	default:
	}
	close(writer.done)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("write after clear: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("write did not complete after clear")
	}
}

func TestCancelClosesPendingRoundTrip(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	stop := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			<-stop
			_ = conn.Close()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient(context.Background(), testDialer{}, M.ParseSocksaddr(listener.Addr().String()), option.V2RayGRPCOptions{}, nil).(*Client)
	conn, err := client.DialContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	defer conn.Close()
	done := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("probe")); done <- err }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled write remained blocked")
	}
	close(stop)
}

func TestCloseUnblocksReadBeforeHeaders(t *testing.T) {
	writer := &blockingWriter{done: make(chan struct{}), started: make(chan struct{})}
	conn := newLateGunConn(writer, writer, nil)
	readDone := make(chan error, 1)
	go func() { _, err := conn.Read(make([]byte, 1)); readDone <- err }()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("Read remained blocked after Close")
	}
}
