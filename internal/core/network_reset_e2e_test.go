//go:build integration && singbox

package core

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing/service"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

func TestVLESSWebSocketNetworkReset(t *testing.T) {
	target := startTargetServer(t)
	serverPort := freePort(t)
	startInstanceAlive(t, fmt.Appendf(nil, `{
		"log": {"disabled":true},
		"inbounds": [{"type":"vless","listen":"127.0.0.1","listen_port":%d,
			"users":[{"uuid":%q}],"transport":{"type":"ws","path":"/loopback"}}],
		"outbounds": [{"type":"direct","tag":"direct"}]
	}`, serverPort, e2eUUID))

	profile, err := integrationReg.Parse(fmt.Sprintf("vless://%s@127.0.0.1:%d?type=ws&path=%%2Floopback", e2eUUID, serverPort))
	if err != nil {
		t.Fatal(err)
	}
	ports := singbox.Ports{Socks: freePort(t), HTTP: freePort(t)}
	cfg, err := singbox.GenerateProxyConfigOpts(integrationReg, []protocol.Profile{profile}, singbox.ProxyOpts{Ports: ports})
	if err != nil {
		t.Fatal(err)
	}
	// Force the loopback origin through VLESS, including traffic to private IPs.
	cfg.Route.Rules = nil
	cfg.Log.Disabled = true
	data, err := singbox.MarshalIndented(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := service.ContextWithDefaultRegistry(t.Context())
	clientCore, err := newBoxCore(ctx, "proxy", data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientCore.Close() })
	if err := clientCore.Start(ctx); err != nil {
		t.Fatal(err)
	}

	check := func(phase string) {
		t.Helper()
		for _, inbound := range []struct {
			scheme string
			port   int
		}{{"http", ports.HTTP}, {"socks5", ports.Socks}} {
			proxyURL := &url.URL{Scheme: inbound.scheme, Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(inbound.port))}
			client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true,
			}}
			resp, err := client.Get("http://" + target + "/loopback")
			if err != nil {
				t.Fatalf("%s via %s: %v", phase, inbound.scheme, err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK || string(body) != "singctl-xhttp-ok" {
				t.Fatalf("%s via %s: status=%s, body=%q, err=%v", phase, inbound.scheme, resp.Status, body, err)
			}
		}
	}
	check("before reset")
	clientCore.ResetNetwork()
	check("after reset")

	// Hold one tracked connection inside CloseAll so both probes run while
	// the real network reset is still in progress. No sampling or sleeps.
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	left, right := net.Pipe()
	defer right.Close()
	tracker := service.FromContext[adapter.ConnectionManager](ctx)
	tracker.TrackConn(&resetBarrierConn{Conn: left, onClose: func() {
		close(started)
		<-release
	}})
	go func() {
		clientCore.ResetNetwork()
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("network reset never started closing connections")
	}
	check("during reset")

	// MODE off must stop accepting new work even while the detached old
	// connection remains blocked in Close.
	closed := make(chan error, 1)
	go func() { closed <- clientCore.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close remained blocked behind the detached connection")
	}
	for _, port := range []int{ports.HTTP, ports.Socks} {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
		if err == nil {
			conn.Close()
			t.Fatalf("listener on port %d accepted a connection after Close", port)
		}
	}
	if err := clientCore.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}

	replacement, err := newBoxCore(ctx, "proxy-replacement", data)
	if err != nil {
		t.Fatalf("new proxy core: %v", err)
	}
	t.Cleanup(func() { _ = replacement.Close() })
	if err := replacement.Start(ctx); err != nil {
		t.Fatalf("start replacement: %v", err)
	}
	check("replacement before old reset release")

	unblock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("network reset did not finish")
	}
	check("replacement after old reset release")
}

type resetBarrierConn struct {
	net.Conn
	onClose func()
}

func (c *resetBarrierConn) Close() error {
	c.onClose()
	return c.Conn.Close()
}
