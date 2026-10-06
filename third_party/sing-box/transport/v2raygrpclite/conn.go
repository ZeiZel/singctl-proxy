package v2raygrpclite

import (
	std_bufio "bufio"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/baderror"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	"github.com/sagernet/sing/common/varbin"
)

// kanged from: https://github.com/Qv2ray/gun-lite

var _ net.Conn = (*GunConn)(nil)

type GunConn struct {
	mu              sync.Mutex
	rawReader       io.Reader
	requestReader   io.Closer
	reader          *std_bufio.Reader
	writer          io.Writer
	flusher         http.Flusher
	create          chan struct{}
	err             error
	readRemaining   int
	cancel          context.CancelFunc
	closeOnce       sync.Once
	createOnce      sync.Once
	writeDeadline   time.Time
	deadlineChanged chan struct{}
	closed          bool
}

func newGunConn(reader io.Reader, writer io.Writer, flusher http.Flusher) *GunConn {
	return &GunConn{
		rawReader:       reader,
		reader:          std_bufio.NewReader(reader),
		writer:          writer,
		flusher:         flusher,
		deadlineChanged: make(chan struct{}),
	}
}

func newLateGunConn(writer io.Writer, requestReader io.Closer, cancel context.CancelFunc) *GunConn {
	return &GunConn{
		create:          make(chan struct{}),
		writer:          writer,
		requestReader:   requestReader,
		cancel:          cancel,
		deadlineChanged: make(chan struct{}),
	}
}

func (c *GunConn) setup(reader io.Reader, err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = common.Close(reader)
		c.signalCreate()
		return
	}
	if reader != nil {
		c.rawReader = reader
		c.reader = std_bufio.NewReader(reader)
	}
	c.err = err
	c.mu.Unlock()
	c.signalCreate()
}

func (c *GunConn) signalCreate() {
	if c.create != nil {
		c.createOnce.Do(func() { close(c.create) })
	}
}

func (c *GunConn) Read(b []byte) (n int, err error) {
	n, err = c.read(b)
	return n, baderror.WrapH2(err)
}

func (c *GunConn) read(b []byte) (n int, err error) {
	c.mu.Lock()
	reader, readErr := c.reader, c.err
	c.mu.Unlock()
	if reader == nil {
		<-c.create
		c.mu.Lock()
		reader, readErr = c.reader, c.err
		c.mu.Unlock()
		if readErr != nil {
			return 0, readErr
		}
		if reader == nil {
			return 0, net.ErrClosed
		}
	}

	if c.readRemaining > 0 {
		if len(b) > c.readRemaining {
			b = b[:c.readRemaining]
		}
		n, err = reader.Read(b)
		c.readRemaining -= n
		return
	}

	_, err = reader.Discard(6)
	if err != nil {
		return
	}

	dataLen, err := binary.ReadUvarint(reader)
	if err != nil {
		return
	}

	readLen := int(dataLen)
	c.readRemaining = readLen
	if len(b) > readLen {
		b = b[:readLen]
	}

	n, err = reader.Read(b)
	c.readRemaining -= n
	return
}

func (c *GunConn) Write(b []byte) (n int, err error) {
	varLen := varbin.UvarintLen(uint64(len(b)))
	frame := buf.NewSize(6 + varLen + len(b))
	header := frame.Extend(6 + varLen)
	header[0] = 0x00
	binary.BigEndian.PutUint32(header[1:5], uint32(1+varLen+len(b)))
	header[5] = 0x0A
	binary.PutUvarint(header[6:], uint64(len(b)))
	common.Must1(frame.Write(b))
	defer frame.Release()
	if err := c.writeFrame(frame.Bytes()); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (c *GunConn) writeFrame(frame []byte) error {
	// The caller may release a pooled buf.Buffer as soon as this method returns;
	// keep an immutable frame for the asynchronous writer.
	frameCopy := append([]byte(nil), frame...)
	resultCh := make(chan error, 1)
	go func() {
		_, err := c.writer.Write(frameCopy)
		resultCh <- err
	}()
	for {
		c.mu.Lock()
		deadline, changed := c.writeDeadline, c.deadlineChanged
		c.mu.Unlock()
		if deadline.IsZero() {
			select {
			case err := <-resultCh:
				if err != nil {
					return baderror.WrapH2(err)
				}
				return c.flush()
			case <-changed:
			}
			continue
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			c.abort()
			return os.ErrDeadlineExceeded
		}
		timer := time.NewTimer(remaining)
		select {
		case err := <-resultCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if err != nil {
				return baderror.WrapH2(err)
			}
			return c.flush()
		case <-changed:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
			c.mu.Lock()
			stillExpired := c.writeDeadline.Equal(deadline)
			c.mu.Unlock()
			if !stillExpired {
				continue
			}
			c.abort()
			return os.ErrDeadlineExceeded
		}
	}
}

func (c *GunConn) flush() error {
	if c.flusher != nil {
		c.flusher.Flush()
	}
	return nil
}

func (c *GunConn) WriteBuffer(buffer *buf.Buffer) error {
	defer buffer.Release()
	dataLen := buffer.Len()
	varLen := varbin.UvarintLen(uint64(dataLen))
	header := buffer.ExtendHeader(6 + varLen)
	header[0] = 0x00
	binary.BigEndian.PutUint32(header[1:5], uint32(1+varLen+dataLen))
	header[5] = 0x0A
	binary.PutUvarint(header[6:], uint64(dataLen))
	return c.writeFrame(buffer.Bytes())
}

func (c *GunConn) FrontHeadroom() int {
	return 6 + binary.MaxVarintLen64
}

func (c *GunConn) Close() error {
	var err error
	c.closeOnce.Do(func() { err = c.abort() })
	return err
}

func (c *GunConn) abort() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.signalCreate()
	if c.cancel != nil {
		c.cancel()
	}
	return common.Close(c.requestReader, c.rawReader, c.writer)
}

func (c *GunConn) LocalAddr() net.Addr {
	return M.Socksaddr{}
}

func (c *GunConn) RemoteAddr() net.Addr {
	return M.Socksaddr{}
}

func (c *GunConn) SetDeadline(t time.Time) error {
	return c.SetWriteDeadline(t)
}

func (c *GunConn) SetReadDeadline(t time.Time) error {
	return os.ErrInvalid
}

func (c *GunConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDeadline = t
	close(c.deadlineChanged)
	c.deadlineChanged = make(chan struct{})
	c.mu.Unlock()
	return nil
}

func (c *GunConn) NeedAdditionalReadDeadline() bool {
	return true
}
