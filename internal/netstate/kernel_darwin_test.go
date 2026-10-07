//go:build darwin

package netstate

import (
	"context"
	"errors"
	"golang.org/x/net/route"
	"syscall"
	"testing"
	"time"
)

func fixtureDefault(name string, scoped bool) *route.RouteMessage {
	flags := syscall.RTF_UP
	if scoped {
		flags |= syscall.RTF_IFSCOPE
	}
	addrs := make([]route.Addr, syscall.RTAX_MAX)
	addrs[syscall.RTAX_DST] = &route.Inet4Addr{}
	addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{}
	addrs[syscall.RTAX_GATEWAY] = &route.Inet4Addr{IP: [4]byte{192, 168, 1, 1}}
	addrs[syscall.RTAX_IFP] = &route.LinkAddr{Name: name}
	return &route.RouteMessage{Type: syscall.RTM_GET, Flags: flags, Addrs: addrs}
}

func fixtureDefaultImplicitMask(name string, index int, scoped bool) *route.RouteMessage {
	flags := syscall.RTF_UP
	if scoped {
		flags |= syscall.RTF_IFSCOPE
	}
	addrs := make([]route.Addr, syscall.RTAX_IFP)
	addrs[syscall.RTAX_DST] = &route.Inet4Addr{}
	addrs[syscall.RTAX_GATEWAY] = &route.Inet4Addr{IP: [4]byte{192, 168, 1, 1}}
	if name != "" || index != 0 {
		addrs = append(addrs, &route.LinkAddr{Name: name, Index: index})
	}
	return &route.RouteMessage{Type: syscall.RTM_GET, Flags: flags, Index: index, Addrs: addrs}
}

func TestKernelSnapshotFixtures(t *testing.T) {
	for _, tc := range []struct {
		name, global string
		ipv4         bool
		own          bool
		active, owns bool
	}{
		{"full", "utun4", true, false, true, true}, {"split", "en0", true, false, true, false},
		{"ourTun", "utun5", true, true, false, false}, {"transientWithoutIPv4", "utun4", false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tun := "utun4"
			ip := "10.1.1.1"
			if tc.own {
				tun = "utun5"
				ip = "198.18.0.1"
			}
			info := IfaceInfo{Name: tun, NoARP: true}
			if tc.ipv4 {
				info.IPv4 = []string{ip}
			}
			msgs := []route.Message{fixtureDefault(tc.global, false), fixtureDefault("en0", true)}
			state := (&kernelDetector{ourAddrPrefix: OurTunAddrPrefix}).snapshot(map[string]IfaceInfo{tun: info}, parseKernelRoutes(msgs))
			if state.PhysicalIface != "en0" || state.DefaultRouteIface != tc.global || state.CiscoActive != tc.active || state.CiscoOwnsDefault != tc.owns {
				t.Fatalf("snapshot=%+v", state)
			}
		})
	}
	invalid := fixtureDefault("en0", false)
	invalid.Addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{IP: [4]byte{255, 0, 0, 0}}
	if len(parseKernelRoutes([]route.Message{invalid})) != 0 {
		t.Fatal("accepted non-default mask")
	}
	implicit := fixtureDefaultImplicitMask("en0", 7, false)
	if got := parseKernelRoutes([]route.Message{implicit}, map[int]string{7: "en0"}); len(got) != 1 || got[0].iface != "en0" {
		t.Fatalf("implicit /0 route=%+v", got)
	}
	short := fixtureDefaultImplicitMask("", 0, false)
	short.Index = 7
	short.Addrs = short.Addrs[:syscall.RTAX_DST+1]
	if got := parseKernelRoutes([]route.Message{short}, map[int]string{7: "en0"}); len(got) != 1 || got[0].iface != "en0" {
		t.Fatalf("short route index fallback=%+v", got)
	}
}
func TestRouteMessageFilter(t *testing.T) {
	for _, typ := range []int{syscall.RTM_GET, syscall.RTM_ADD, syscall.RTM_DELETE, syscall.RTM_CHANGE} {
		msg := &route.RouteMessage{Type: typ}
		want := typ != syscall.RTM_GET
		if routeMessagesRelevant([]route.Message{msg}) != want {
			t.Fatalf("type=%d", typ)
		}
		msg.Flags = syscall.RTF_HOST
		if routeMessagesRelevant([]route.Message{msg}) {
			t.Fatalf("host type=%d", typ)
		}
		msg.Flags = 0
		msg.Err = syscall.ESRCH
		if routeMessagesRelevant([]route.Message{msg}) {
			t.Fatalf("failed type=%d", typ)
		}
	}
	if routeMessagesRelevant([]route.Message{&route.InterfaceMulticastAddrMessage{}}) {
		t.Fatal("multicast woke monitor")
	}
	if !routeMessagesRelevant([]route.Message{&route.InterfaceMessage{}}) || !routeEventRelevant([]byte{1, 2}) {
		t.Fatal("interface or malformed message lost")
	}
}
func TestWatcherRecoveryOwnership(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads, opens := 0, 0
	closed := map[int]int{}
	watchRouteLoop(ctx, 10, make(chan struct{}, 1), func() (int, error) {
		opens++
		if opens == 1 {
			return -1, errors.New("open failed")
		}
		cancel()
		return 11, nil
	}, func(fd int, b []byte) (int, error) {
		reads++
		if fd != 10 {
			t.Fatalf("read reused fd=%d", fd)
		}
		return 0, syscall.EIO
	}, func(fd int) error { closed[fd]++; return nil }, func(int) error { return nil })
	if reads != 1 || opens != 2 || closed[10] != 1 || closed[11] != 1 {
		t.Fatalf("reads=%d opens=%d closed=%v", reads, opens, closed)
	}
}
func TestWatcherCancelWhileWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	closed := 0
	go func() {
		watchRouteLoop(ctx, 10, make(chan struct{}, 1), func() (int, error) { t.Error("unexpected reopen"); return -1, syscall.EIO }, func(int, []byte) (int, error) { t.Error("read after cancellation"); return 0, nil }, func(int) error { closed++; return nil }, func(int) error { <-ctx.Done(); return nil })
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancel did not finish")
	}
	if closed != 1 {
		t.Fatalf("closed=%d", closed)
	}
}

func TestRouteSocketCloseOnExec(t *testing.T) {
	fd, err := openRouteSocket()
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
	if errno != 0 || flags&syscall.FD_CLOEXEC == 0 {
		t.Fatalf("flags=%d errno=%v", flags, errno)
	}
}
func TestKernelRouteIndexFallback(t *testing.T) {
	msg := fixtureDefault("", false)
	msg.Index = 7
	got := parseKernelRoutes([]route.Message{msg}, map[int]string{7: "en0"})
	if len(got) != 1 || got[0].iface != "en0" {
		t.Fatalf("index fallback=%+v", got)
	}
}
