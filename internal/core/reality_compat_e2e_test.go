//go:build integration && singbox

package core

// These tests deliberately use only loopback sockets.  The Xray binaries are
// selected through XRAY_BIN_<version> (for example XRAY_BIN_26_7_11), so a
// developer can run the matrix without contacting a production endpoint.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/proxy"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

const realityGRPCService = "singctl-reality-grpc"

func TestREALITYCompatibilityMatrix(t *testing.T) {
	for _, version := range []string{"26_7_11", "26_7_28", "26_9_9"} {
		bin := os.Getenv("XRAY_BIN_" + version)
		if bin == "" {
			t.Logf("set XRAY_BIN_%s to run this matrix entry", version)
			continue
		}
		if _, err := os.Stat(bin); err != nil {
			t.Fatalf("XRAY_BIN_%s is not usable: %v", version, err)
		}
		for _, transport := range []string{"tcp", "grpc", "xhttp"} {
			t.Run(version+"/"+transport, func(t *testing.T) {
				target := startTargetServer(t)
				dest := startTLSDest(t)
				xrayPort, socksPort := freePort(t), freePort(t)
				startRealityXray(t, bin, xrayPort, dest, transport, "1.0.0")
				startInstanceAlive(t, realityClientConfig(t, socksPort, xrayPort, transport, ""))
				if body := getThroughSocks(t, socksPort, "http://"+target+"/reality"); body != "singctl-xhttp-ok" {
					t.Fatalf("unexpected body through REALITY %s: %q", transport, body)
				}
			})
		}
	}
}

// TestREALITYOldClientVersionRejected locks down the server-side compatibility
// boundary. Xray's default minimum is v26.3.27; a deliberately old client
// version must fail quickly instead of being reported as a successful probe.
func TestREALITYOldClientVersionRejected(t *testing.T) {
	bin := os.Getenv("XRAY_BIN_26_7_11")
	if bin == "" {
		t.Skip("set XRAY_BIN_26_7_11 to run the REALITY version rejection test")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("XRAY_BIN_26_7_11 is not usable: %v", err)
	}
	xrayPort, socksPort := freePort(t), freePort(t)
	startRealityXray(t, bin, xrayPort, startTLSDest(t), "tcp", "")
	startInstanceAlive(t, realityClientConfig(t, socksPort, xrayPort, "tcp", "1.0.0"))

	target := startTargetServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := getThroughSocksContext(ctx, socksPort, "http://"+target+"/reality"); err == nil {
		t.Fatal("old REALITY client version unexpectedly connected")
	}
}

func TestREALITYDefaultMinClientVersion(t *testing.T) {
	bin := os.Getenv("XRAY_BIN_26_7_11")
	if bin == "" {
		t.Skip("set XRAY_BIN_26_7_11 to run the REALITY default-minClientVer test")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("XRAY_BIN_26_7_11 is not usable: %v", err)
	}
	target := startTargetServer(t)
	xrayPort, socksPort := freePort(t), freePort(t)
	startRealityXray(t, bin, xrayPort, startTLSDest(t), "tcp", "")
	startInstanceAlive(t, realityClientConfig(t, socksPort, xrayPort, "tcp", ""))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := getThroughSocksContext(ctx, socksPort, "http://"+target+"/reality"); err != nil {
		t.Fatalf("default client version rejected by Xray default minClientVer: %v", err)
	}
}

func TestREALITYGRPCWrongShortIDRejected(t *testing.T) {
	bin := os.Getenv("XRAY_BIN_26_7_11")
	if bin == "" {
		t.Skip("set XRAY_BIN_26_7_11 to run the REALITY wrong-short-id test")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("XRAY_BIN_26_7_11 is not usable: %v", err)
	}
	xrayPort, socksPort := freePort(t), freePort(t)
	startRealityXray(t, bin, xrayPort, startTLSDest(t), "grpc", "1.0.0")
	startInstanceAlive(t, realityClientConfigWithShortID(t, socksPort, xrayPort, "grpc", "", "deadbeef"))
	target := startTargetServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := getThroughSocksContext(ctx, socksPort, "http://"+target+"/reality"); err == nil {
		t.Fatal("REALITY gRPC connection with wrong short ID unexpectedly succeeded")
	}
}

func TestREALITYGRPCFailurePublishedToClashAPI(t *testing.T) {
	bin := os.Getenv("XRAY_BIN_26_7_11")
	if bin == "" {
		t.Skip("set XRAY_BIN_26_7_11 to run the Clash API REALITY diagnostic test")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("XRAY_BIN_26_7_11 is not usable: %v", err)
	}
	xrayPort, socksPort, clashPort := freePort(t), freePort(t), freePort(t)
	target := startTargetServer(t)
	startRealityXray(t, bin, xrayPort, startTLSDest(t), "grpc", "1.0.0")
	startInstanceAlive(t, realityClientConfigWithClash(t, socksPort, xrayPort, "grpc", "", "deadbeef", clashPort))
	waitForPort(t, clashPort)

	delayURL := fmt.Sprintf("http://127.0.0.1:%d/proxies/proxy/delay?url=%s&timeout=2000", clashPort, url.QueryEscape("http://"+target+"/reality"))
	resp, err := http.Get(delayURL)
	if err != nil {
		t.Fatalf("GET Clash delay: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("delay status = %d, want 503; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "REALITY") || !strings.Contains(string(body), "minClientVer") {
		t.Fatalf("delay diagnostic = %s, want REALITY/minClientVer hint", body)
	}

	proxiesResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/proxies/proxy", clashPort))
	if err != nil {
		t.Fatalf("GET Clash proxy: %v", err)
	}
	var proxyState struct {
		History []struct {
			Delay int    `json:"delay"`
			Error string `json:"error"`
		} `json:"history"`
	}
	if err := json.NewDecoder(proxiesResp.Body).Decode(&proxyState); err != nil {
		proxiesResp.Body.Close()
		t.Fatalf("decode proxy state: %v", err)
	}
	proxiesResp.Body.Close()
	if len(proxyState.History) == 0 || proxyState.History[len(proxyState.History)-1].Delay != 0 || !strings.Contains(proxyState.History[len(proxyState.History)-1].Error, "REALITY") {
		t.Fatalf("proxy history = %+v, want delay=0 with REALITY error", proxyState.History)
	}
}

func realityClientConfig(t *testing.T, socksPort, xrayPort int, transport, clientVersion string) []byte {
	return realityClientConfigWithShortID(t, socksPort, xrayPort, transport, clientVersion, "4d04")
}

func realityClientConfigWithShortID(t *testing.T, socksPort, xrayPort int, transport, clientVersion, shortID string) []byte {
	return realityClientConfigWithClash(t, socksPort, xrayPort, transport, clientVersion, shortID, 0)
}

func realityClientConfigWithClash(t *testing.T, socksPort, xrayPort int, transport, clientVersion, shortID string, clashPort int) []byte {
	t.Helper()
	query := fmt.Sprintf("type=%s&security=reality&pbk=%s&sid=%s&sni=xhttp.test&fp=chrome", transport, realityPublicKey, shortID)
	if transport == "grpc" {
		query += "&serviceName=" + realityGRPCService
	} else if transport == "xhttp" {
		query += "&path=%2Fxh"
	}
	if clientVersion != "" {
		query += "&realityClientVersion=" + clientVersion
	}
	profile, err := integrationReg.Parse(fmt.Sprintf("vless://%s@127.0.0.1:%d?%s", e2eUUID, xrayPort, query))
	if err != nil {
		t.Fatalf("parse synthetic REALITY URI: %v", err)
	}
	module, ok := integrationReg.Module(protocol.Name("vless"))
	if !ok {
		t.Fatal("vless module is not registered")
	}
	renderer, ok := module.(singbox.Renderer)
	if !ok {
		t.Fatalf("vless module %T does not render sing-box nodes", module)
	}
	node, err := renderer.RenderNode(profile, singbox.RenderOpts{Tag: "proxy"})
	if err != nil {
		t.Fatalf("render synthetic REALITY URI: %v", err)
	}
	outboundJSON, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal rendered REALITY outbound: %v", err)
	}
	config := fmt.Appendf(nil, `{
  "log":{"level":"warn"},
  "inbounds":[{"type":"socks","tag":"in","listen":"127.0.0.1","listen_port":%d}],
  "outbounds":[%s],
  "route":{"final":"proxy"}
}`, socksPort, outboundJSON)
	if clashPort == 0 {
		return config
	}
	var doc map[string]any
	if err := json.Unmarshal(config, &doc); err != nil {
		t.Fatalf("decode generated config: %v", err)
	}
	doc["experimental"] = map[string]any{"clash_api": map[string]any{"external_controller": fmt.Sprintf("127.0.0.1:%d", clashPort)}}
	config, err = json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal Clash API config: %v", err)
	}
	return config
}

func startRealityXray(t *testing.T, bin string, port, destPort int, transport, minClientVersion string) {
	t.Helper()
	dir := t.TempDir()
	stream := fmt.Sprintf(`"network":%q,"security":"reality","realitySettings":{"show":false,"dest":"127.0.0.1:%d","serverNames":["xhttp.test"],"privateKey":%q,"shortIds":["4d04"]`, transport, destPort, realityPrivateKey)
	if minClientVersion != "" {
		stream += fmt.Sprintf(`,"minClientVer":%q`, minClientVersion)
	}
	stream += "}"
	if transport == "grpc" {
		stream += fmt.Sprintf(`,"grpcSettings":{"serviceName":%q}`, realityGRPCService)
	} else if transport == "xhttp" {
		stream += `,"xhttpSettings":{"path":"/xh","mode":"auto"}`
	}
	cfg := fmt.Sprintf(`{"log":{"loglevel":"warning"},"inbounds":[{"listen":"127.0.0.1","port":%d,"protocol":"vless","settings":{"clients":[{"id":%q}],"decryption":"none"},"streamSettings":{%s}}],"outbounds":[{"protocol":"freedom","settings":{"finalRules":[{"action":"allow","ip":["127.0.0.0/8"]}]}}]}`,
		port, e2eUUID, stream)
	path := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "run", "-c", path)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start xray: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitForPort(t, port)
}

func getThroughSocksContext(ctx context.Context, socksPort int, rawURL string) error {
	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort), nil, proxy.Direct)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
		return dialer.Dial(network, addr)
	}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do((&http.Request{Method: http.MethodGet, URL: mustURL(rawURL)}).WithContext(ctx))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}
