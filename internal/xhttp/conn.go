package xhttp

import (
	"context"
	"io"
	"net"
	"sync"
	"time"
)

// streamConn presents an XHTTP session as a net.Conn: reads come from the
// downlink response body, writes go to the uplink (a request body in stream-up
// / stream-one mode, a series of numbered POSTs in packet-up mode).
//
// Deadlines are not enforced, matching Xray's own splitConn: there is no
// deadline to set on an in-flight HTTP request body. SetDeadline reports
// success rather than an error because callers above us (crypto/tls, net/http)
// treat a deadline failure as fatal, and they all also honour context
// cancellation, which does propagate here.
type streamConn struct {
	reader     io.ReadCloser
	writer     io.WriteCloser
	remoteAddr net.Addr
	localAddr  net.Addr
	onClose    func()
	closeOnce  sync.Once
}

func (c *streamConn) Read(b []byte) (int, error) {
	if c.reader == nil {
		return 0, io.EOF
	}
	return c.reader.Read(b)
}

func (c *streamConn) Write(b []byte) (int, error) {
	if c.writer == nil {
		return 0, io.ErrClosedPipe
	}
	return c.writer.Write(b)
}

func (c *streamConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		if c.onClose != nil {
			c.onClose()
		}
		if c.writer != nil {
			err = c.writer.Close()
		}
		if c.reader != nil {
			if rerr := c.reader.Close(); err == nil {
				err = rerr
			}
		}
	})
	return err
}

func (c *streamConn) LocalAddr() net.Addr {
	if c.localAddr != nil {
		return c.localAddr
	}
	return placeholderAddr{}
}

func (c *streamConn) RemoteAddr() net.Addr {
	if c.remoteAddr != nil {
		return c.remoteAddr
	}
	return placeholderAddr{}
}

func (c *streamConn) SetDeadline(time.Time) error      { return nil }
func (c *streamConn) SetReadDeadline(time.Time) error  { return nil }
func (c *streamConn) SetWriteDeadline(time.Time) error { return nil }

// placeholderAddr stands in when httptrace never reported a connection (for
// example when an HTTP/2 stream reused a pooled connection opened earlier).
type placeholderAddr struct{}

func (placeholderAddr) Network() string { return "tcp" }
func (placeholderAddr) String() string  { return "xhttp" }

// pendingReader lets DialContext return before the downlink response headers
// have arrived: reads block until the body shows up (or the request fails).
// Closing it also cancels the underlying HTTP request, so a stream abandoned
// before its response landed does not leave a request in flight forever.
type pendingReader struct {
	ready  chan struct{}
	once   sync.Once
	cancel context.CancelFunc

	body io.ReadCloser
	err  error
}

func (r *pendingReader) set(body io.ReadCloser) {
	r.once.Do(func() {
		r.body = body
		close(r.ready)
	})
	if r.body != body {
		// Lost the race against fail/Close; don't leak the response body.
		body.Close()
	}
}

func (r *pendingReader) fail(err error) {
	r.once.Do(func() {
		r.err = err
		close(r.ready)
	})
}

func (r *pendingReader) Read(b []byte) (int, error) {
	<-r.ready
	if r.err != nil {
		return 0, r.err
	}
	if r.body == nil {
		return 0, io.EOF
	}
	return r.body.Read(b)
}

func (r *pendingReader) Close() error {
	r.fail(net.ErrClosed)
	if r.cancel != nil {
		r.cancel()
	}
	if r.body != nil {
		return r.body.Close()
	}
	return nil
}

// packetUploader is the packet-up uplink: it batches Write calls into chunks of
// at most scMaxEachPostBytes and ships each as its own numbered request. The
// batching is what makes the mode usable — one request per Write would cap
// throughput at a handful of KB per round trip.
type packetUploader struct {
	dialer    *Dialer
	sessionID string
	ctx       context.Context
	cancel    context.CancelFunc

	maxChunk  int
	minWaitMs Range

	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
	err    error

	done chan struct{}
}

func newPacketUploader(ctx context.Context, d *Dialer, sessionID string) *packetUploader {
	// The uploader outlives the dial context: it must keep posting for as long
	// as the stream is open, and is torn down by stop().
	upCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	u := &packetUploader{
		dialer:    d,
		sessionID: sessionID,
		ctx:       upCtx,
		cancel:    cancel,
		maxChunk:  int(d.settings.maxEachPostBytes().rand()),
		minWaitMs: d.settings.minPostsIntervalMs(),
		done:      make(chan struct{}),
	}
	if u.maxChunk <= 0 {
		u.maxChunk = 1000000
	}
	u.cond = sync.NewCond(&u.mu)
	go u.run()
	return u
}

func (u *packetUploader) Write(b []byte) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	written := 0
	for len(b) > 0 {
		// Backpressure: never let more than one full chunk queue up, so a fast
		// writer cannot outrun the POSTs and buffer the whole transfer in RAM.
		for len(u.buf) >= u.maxChunk && !u.closed && u.err == nil {
			u.cond.Wait()
		}
		if u.closed {
			return written, io.ErrClosedPipe
		}
		if u.err != nil {
			return written, u.err
		}
		n := min(u.maxChunk-len(u.buf), len(b))
		u.buf = append(u.buf, b[:n]...)
		b = b[n:]
		written += n
		u.cond.Broadcast()
	}
	return written, nil
}

func (u *packetUploader) Close() error {
	u.stop()
	return nil
}

// stop marks the uploader closed and waits for the in-flight chunk to drain.
func (u *packetUploader) stop() {
	u.mu.Lock()
	if u.closed {
		u.mu.Unlock()
		<-u.done
		return
	}
	u.closed = true
	u.cond.Broadcast()
	u.mu.Unlock()
	<-u.done
	u.cancel()
}

// run drains the buffer into sequenced uploads until the stream is closed or an
// upload fails. Uploads are issued one at a time and in order: the server
// reassembles by sequence number, but keeping the wire order removes any
// dependence on its reorder window (scMaxBufferedPosts).
func (u *packetUploader) run() {
	defer close(u.done)
	var (
		seq       int64
		lastWrite time.Time
	)
	for {
		u.mu.Lock()
		for len(u.buf) == 0 && !u.closed && u.err == nil {
			u.cond.Wait()
		}
		if u.err != nil || (u.closed && len(u.buf) == 0) {
			u.mu.Unlock()
			return
		}
		n := min(u.maxChunk, len(u.buf))
		chunk := make([]byte, n)
		copy(chunk, u.buf[:n])
		u.buf = u.buf[n:]
		u.cond.Broadcast()
		u.mu.Unlock()

		// scMinPostsIntervalMs paces the POSTs so the traffic pattern does not
		// look like a burst of identical requests.
		if wait := u.minWaitMs.rand(); wait > 0 && !lastWrite.IsZero() {
			if remaining := time.Duration(wait)*time.Millisecond - time.Since(lastWrite); remaining > 0 {
				select {
				case <-time.After(remaining):
				case <-u.ctx.Done():
					return
				}
			}
		}
		lastWrite = time.Now()

		if err := u.dialer.postPacket(u.ctx, u.sessionID, seq, chunk); err != nil {
			u.mu.Lock()
			if u.err == nil {
				u.err = err
			}
			u.cond.Broadcast()
			u.mu.Unlock()
			return
		}
		seq++
	}
}
