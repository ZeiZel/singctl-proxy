//go:build integration && singbox

// End-to-end test of the XHTTP transport against the REFERENCE implementation:
// a real Xray-core server. The golden/decode tests only prove our config is
// well-formed; this proves the bytes we put on the wire are the bytes Xray
// expects — the padding, the session/sequence placement, the upload framing and
// the VLESS handshake on top of it all.
//
// It is skipped unless XRAY_BIN points at an xray binary:
//
//	XRAY_BIN=/path/to/xray go test -tags "integration singbox with_utls with_clash_api" \
//	    -run TestXHTTP_EndToEnd ./internal/core/
package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/net/proxy"
)

const e2eUUID = "4ce58870-27d3-489b-87a0-3109db4fb919"

func TestXHTTP_EndToEnd(t *testing.T) {
	xrayBin := requireXray(t)

	target := startTargetServer(t)

	for _, tc := range []struct {
		name string
		mode string
		tls  bool
	}{
		{"packet-up/http1.1", "packet-up", false},
		{"stream-up/http1.1", "stream-up", false},
		{"stream-one/http1.1", "stream-one", false},
		{"packet-up/h2", "packet-up", true},
		{"stream-up/h2", "stream-up", true},
		{"stream-one/h2", "stream-one", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			xrayPort := freePort(t)
			socksPort := freePort(t)
			startXray(t, xrayBin, xrayPort, tc.tls)

			cfg := e2eProxyConfig(socksPort, xrayPort, tc.mode, tc.tls)
			startInstanceAlive(t, cfg)

			body := getThroughSocks(t, socksPort, "http://"+target+"/hello")
			if body != "singctl-xhttp-ok" {
				t.Fatalf("unexpected body through the tunnel: %q", body)
			}
		})
	}
}

// e2eProxyConfig is a minimal proxy config: one socks inbound, our vless-xhttp
// outbound, everything routed through it. It deliberately omits the generated
// config's ip_is_private→direct rule, which would otherwise short-circuit the
// loopback test target past the tunnel.
func e2eProxyConfig(socksPort, xrayPort int, mode string, useTLS bool) []byte {
	tlsBlock := ""
	if useTLS {
		tlsBlock = `,"tls":{"enabled":true,"server_name":"xhttp.test","insecure":true,"alpn":["h2"]}`
	}
	return fmt.Appendf(nil, `{
  "log": {"level": "warn"},
  "inbounds": [{"type":"socks","tag":"in","listen":"127.0.0.1","listen_port":%d}],
  "outbounds": [
    {"type":"vless-xhttp","tag":"proxy","server":"127.0.0.1","server_port":%d,"uuid":%q,
     "xhttp":{"path":"/xh","mode":%q}%s}
  ],
  "route": {"final": "proxy"}
}`, socksPort, xrayPort, e2eUUID, mode, tlsBlock)
}

// startXray writes a matching Xray server config and runs the reference server.
func startXray(t *testing.T, bin string, port int, useTLS bool) {
	t.Helper()
	dir := t.TempDir()

	stream := `"streamSettings":{"network":"xhttp","xhttpSettings":{"path":"/xh","mode":"auto"}}`
	if useTLS {
		certFile, keyFile := writeSelfSignedCert(t, dir)
		stream = fmt.Sprintf(`"streamSettings":{"network":"xhttp","security":"tls",`+
			`"tlsSettings":{"alpn":["h2"],"certificates":[{"certificateFile":%q,"keyFile":%q}]},`+
			`"xhttpSettings":{"path":"/xh","mode":"auto"}}`, certFile, keyFile)
	}

	cfg := fmt.Sprintf(`{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "listen": "127.0.0.1", "port": %d, "protocol": "vless",
    "settings": {"clients": [{"id": %q}], "decryption": "none"},
    %s
  }],
  "outbounds": [{"protocol": "freedom"}]
}`, port, e2eUUID, stream)

	path := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "run", "-c", path)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start xray: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitForPort(t, port)
}

// startInstanceAlive builds and starts a sing-box instance, leaving it running
// for the duration of the subtest (startInstance closes it immediately).
func startInstanceAlive(t *testing.T, data []byte) {
	t.Helper()
	core, err := newBoxCore(context.Background(), "e2e", data)
	if err != nil {
		t.Fatalf("build core: %v\n%s", err, data)
	}
	if err := core.Start(context.Background()); err != nil {
		t.Fatalf("start core: %v\n%s", err, data)
	}
	t.Cleanup(func() { _ = core.Close() })
}

// startTargetServer is the origin the tunnelled request finally reaches.
func startTargetServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "singctl-xhttp-ok")
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

func getThroughSocks(t *testing.T, socksPort int, url string) string {
	t.Helper()
	body, err := tryGetThroughSocks(socksPort, url)
	if err != nil {
		t.Fatalf("GET through the tunnel: %v", err)
	}
	return body
}

func tryGetThroughSocks(socksPort int, url string) (string, error) {
	dialer, err := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", socksPort), nil, proxy.Direct)
	if err != nil {
		return "", err
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		},
	}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitForPort(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("nothing listening on 127.0.0.1:%d", port)
}

// writeSelfSignedCert produces a throwaway cert for the TLS (HTTP/2) variants;
// the client side sets insecure:true, so only the handshake matters.
func writeSelfSignedCert(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "xhttp.test"},
		DNSNames:     []string{"xhttp.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	writePEM(t, certFile, "CERTIFICATE", der)
	writePEM(t, keyFile, "EC PRIVATE KEY", keyDER)
	return certFile, keyFile
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		t.Fatal(err)
	}
}

// TestXHTTP_EndToEndReality covers the combination the anti-blocking setups
// actually ship: XHTTP under REALITY, where mode "auto" resolves to stream-one
// and the HTTP version is forced to 2. The REALITY server relays the TLS
// handshake to `dest`, so the test points dest at a local TLS server and needs
// no internet access.
func TestXHTTP_EndToEndReality(t *testing.T) {
	xrayBin := requireXray(t)

	target := startTargetServer(t)
	destPort := startTLSDest(t)
	xrayPort := freePort(t)
	socksPort := freePort(t)

	dir := t.TempDir()
	cfg := fmt.Sprintf(`{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "listen": "127.0.0.1", "port": %d, "protocol": "vless",
    "settings": {"clients": [{"id": %q}], "decryption": "none"},
    "streamSettings": {
      "network": "xhttp", "security": "reality",
      "realitySettings": {
        "target": "127.0.0.1:%d", "serverNames": ["xhttp.test"],
        "privateKey": %q, "shortIds": ["4d04"]
      },
      "xhttpSettings": {"path": "/xh", "mode": "auto"}
    }
  }],
  "outbounds": [{"protocol": "freedom"}]
}`, xrayPort, e2eUUID, destPort, realityPrivateKey)

	path := filepath.Join(dir, "xray.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(xrayBin, "run", "-c", path)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start xray: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	waitForPort(t, xrayPort)

	// mode is left empty on purpose: "auto" under REALITY must resolve to
	// stream-one, exactly as Xray's own client does.
	boxCfg := fmt.Appendf(nil, `{
  "log": {"level": "warn"},
  "inbounds": [{"type":"socks","tag":"in","listen":"127.0.0.1","listen_port":%d}],
  "outbounds": [
    {"type":"vless-xhttp","tag":"proxy","server":"127.0.0.1","server_port":%d,"uuid":%q,
     "tls":{"enabled":true,"server_name":"xhttp.test","utls":{"enabled":true,"fingerprint":"chrome"},
            "reality":{"enabled":true,"public_key":%q,"short_id":"4d04"}},
     "xhttp":{"path":"/xh"}}
  ],
  "route": {"final": "proxy"}
}`, socksPort, xrayPort, e2eUUID, realityPublicKey)

	startInstanceAlive(t, boxCfg)
	if body := getThroughSocks(t, socksPort, "http://"+target+"/hello"); body != "singctl-xhttp-ok" {
		t.Fatalf("unexpected body through the REALITY tunnel: %q", body)
	}
}

// Throwaway REALITY keypair, generated with `xray x25519`. It guards nothing:
// both halves live in this test and the server it authenticates is local.
const (
	realityPrivateKey = "wFXSNGtnlQ1MXhN7zX1FKL-Xr7xYX0mkeAJ0Z2I6OHo"
	realityPublicKey  = "ot8nyFmGljyf8UIjBJ2ah1IgC4s3SUtFYla3E_a_8X4"
)

// startTLSDest is the "real site" a REALITY server relays handshakes to.
func startTLSDest(t *testing.T) int {
	t.Helper()
	dir := t.TempDir()
	certFile, keyFile := writeSelfSignedCert(t, dir)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "dest")
	})}
	go srv.ServeTLS(ln, certFile, keyFile)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

// TestXHTTP_EndToEndURLTest puts an XHTTP outbound inside the urltest failover
// group singctl builds for multi-key setups. The first member points at a dead
// port, so the group must health-check both, discard the broken one and carry
// traffic over the live XHTTP server.
func TestXHTTP_EndToEndURLTest(t *testing.T) {
	xrayBin := requireXray(t)

	target := startTargetServer(t)
	livePort := freePort(t)
	deadPort := freePort(t)
	socksPort := freePort(t)
	startXray(t, xrayBin, livePort, false)

	cfg := fmt.Appendf(nil, `{
  "log": {"level": "warn"},
  "inbounds": [{"type":"socks","tag":"in","listen":"127.0.0.1","listen_port":%d}],
  "outbounds": [
    {"type":"vless-xhttp","tag":"proxy-0","server":"127.0.0.1","server_port":%d,"uuid":%q,
     "xhttp":{"path":"/xh","mode":"packet-up"}},
    {"type":"vless-xhttp","tag":"proxy-1","server":"127.0.0.1","server_port":%d,"uuid":%q,
     "xhttp":{"path":"/xh","mode":"packet-up"}},
    {"type":"urltest","tag":"proxy","outbounds":["proxy-0","proxy-1"],
     "url":"http://%s/hello","interval":"1m","tolerance":50}
  ],
  "route": {"final": "proxy"}
}`, socksPort, deadPort, e2eUUID, livePort, e2eUUID, target)

	startInstanceAlive(t, cfg)

	// The group answers with its first member until the first health check
	// completes, so the dead member legitimately serves the opening request;
	// what matters is that it converges onto the live XHTTP one.
	deadline := time.Now().Add(20 * time.Second)
	for {
		body, err := tryGetThroughSocks(socksPort, "http://"+target+"/hello")
		if err == nil && body == "singctl-xhttp-ok" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("urltest group never converged onto the live XHTTP server: body=%q err=%v", body, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// requireXray resolves the reference Xray binary or skips.
//
// A STALE path skips too, rather than failing: XRAY_BIN often points into a
// scratch directory that outlives neither the session nor the machine, and a
// missing reference binary is "this check did not run", not "the code broke".
// Reporting it as a failure once cost a real debugging detour.
func requireXray(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("XRAY_BIN")
	if bin == "" {
		t.Skip("set XRAY_BIN to an xray binary to run the reference end-to-end test")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("XRAY_BIN=%s is not usable (%v); skipping the reference end-to-end test", bin, err)
	}
	return bin
}
