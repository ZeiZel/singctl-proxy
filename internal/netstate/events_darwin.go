//go:build darwin

package netstate

import (
	"context"
	"encoding/binary"
	"os"
	"syscall"
	"time"

	"golang.org/x/net/route"
)

// WatchRouteChanges returns a channel that fires whenever the kernel routing
// table or interface set changes (PF_ROUTE socket). This is the event source
// behind low-latency Cisco detection. Best-effort: an initial socket failure
// returns nil; later read failures reopen with backoff. Read-only and
// unprivileged; never touches Cisco.
func WatchRouteChanges(ctx context.Context) <-chan struct{} {
	fd, err := openRouteSocket()
	if err != nil {
		return nil
	}
	// Cancellation wakes kqueue through a pipe. Only the reader goroutine
	// ever closes the route descriptor, so its number cannot be reused beneath Read.
	wake, cancelWake, err := os.Pipe()
	if err != nil {
		syscall.Close(fd)
		return nil
	}
	syscall.ForkLock.RLock()
	queue, err := syscall.Kqueue()
	if err == nil {
		syscall.CloseOnExec(queue)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		syscall.Close(fd)
		wake.Close()
		cancelWake.Close()
		return nil
	}
	finished := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-finished:
		}
		cancelWake.Close()
	}()
	wait := func(routeFD int) error {
		changes := []syscall.Kevent_t{{Ident: uint64(routeFD), Filter: syscall.EVFILT_READ, Flags: syscall.EV_ADD | syscall.EV_ENABLE}, {Ident: uint64(wake.Fd()), Filter: syscall.EVFILT_READ, Flags: syscall.EV_ADD | syscall.EV_ENABLE}}
		events := make([]syscall.Kevent_t, 2)
		_, err := syscall.Kevent(queue, changes, events, nil)
		return err
	}
	ch := make(chan struct{}, 1)
	go func() {
		defer close(ch)
		defer close(finished)
		defer wake.Close()
		defer syscall.Close(queue)
		watchRouteLoop(ctx, fd, ch, openRouteSocket, syscall.Read, syscall.Close, wait)
	}()
	return ch
}

func watchRouteLoop(ctx context.Context, fd int, ch chan<- struct{}, open func() (int, error), read func(int, []byte) (int, error), closeFD func(int) error, wait func(int) error) {
	defer func() {
		if fd >= 0 {
			closeFD(fd)
		}
	}()
	backoff := 10 * time.Millisecond
	buf := make([]byte, 32<<10)
	for {
		if ctx.Err() != nil {
			return
		}
		err := wait(fd)
		if ctx.Err() != nil {
			return
		}
		n := 0
		if err == nil {
			n, err = read(fd, buf)
		}
		if err == syscall.EINTR || err == syscall.EAGAIN {
			continue
		}
		if err == nil && n > 0 {
			if routeEventRelevant(buf[:n]) {
				select {
				case ch <- struct{}{}:
				default:
				}
			}
			continue
		}
		closeFD(fd)
		fd = -1
		for {
			t := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			next, err := open()
			if err != nil {
				backoff *= 2
				if backoff > time.Second {
					backoff = time.Second
				}
				continue
			}
			fd = next
			if ctx.Err() != nil {
				return
			}
			backoff = 10 * time.Millisecond
			break
		}
	}
}

const routeReceiveBuffer = 256 << 10

func openRouteSocket() (int, error) {
	syscall.ForkLock.RLock()
	fd, err := syscall.Socket(syscall.AF_ROUTE, syscall.SOCK_RAW, 0)
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return -1, err
	}
	// Darwin does not expose SOCK_CLOEXEC in syscall; set FD_CLOEXEC before
	// publishing the descriptor to the watcher.
	if err := syscall.SetNonblock(fd, true); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, routeReceiveBuffer); err != nil {
		_ = syscall.Close(fd)
		return -1, err
	}
	return fd, nil
}

func routeEventRelevant(data []byte) bool {
	if len(data) < 4 {
		return true
	}
	// ParseRIB skips unknown types, so inspect framing before parsing to preserve fail-open semantics.
	for rest := data; len(rest) > 0; {
		if len(rest) < 4 {
			return true
		}
		size := int(binary.NativeEndian.Uint16(rest[:2]))
		if size < 4 || size > len(rest) {
			return true
		}
		switch int(rest[3]) {
		case syscall.RTM_GET, syscall.RTM_GET2, syscall.RTM_MISS, syscall.RTM_LOSING, syscall.RTM_REDIRECT, syscall.RTM_RESOLVE, syscall.RTM_NEWMADDR, syscall.RTM_DELMADDR, syscall.RTM_NEWMADDR2:
		case syscall.RTM_ADD, syscall.RTM_DELETE, syscall.RTM_CHANGE, syscall.RTM_IFINFO, syscall.RTM_IFINFO2, syscall.RTM_NEWADDR, syscall.RTM_DELADDR:
		default:
			return true
		}
		rest = rest[size:]
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, data)
	if err != nil {
		return true
	}
	return routeMessagesRelevant(msgs)
}

func routeMessagesRelevant(msgs []route.Message) bool {
	for _, msg := range msgs {
		switch m := msg.(type) {
		case *route.RouteMessage:
			if m.Err != nil {
				continue
			}
			switch m.Type {
			case syscall.RTM_ADD, syscall.RTM_DELETE, syscall.RTM_CHANGE:
				if m.Flags&syscall.RTF_HOST == 0 {
					return true
				}
			case syscall.RTM_IFINFO, syscall.RTM_IFINFO2, syscall.RTM_NEWADDR, syscall.RTM_DELADDR:
				return true
			}
		case *route.InterfaceMulticastAddrMessage:
			continue
		default:
			return true // fail open for new message types
		}
	}
	return false
}
