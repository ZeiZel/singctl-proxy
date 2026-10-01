//go:build with_utls

package tls

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"golang.org/x/crypto/hkdf"
	"io"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
)

func TestParseRealityClientVersion(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        [3]byte
	}{
		{"default", "", [3]byte{26, 3, 27}},
		{"configured", "1.2.255", [3]byte{1, 2, 255}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRealityClientVersion(tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("parseRealityClientVersion(%q) = %v, %v; want %v", tc.input, got, err, tc.want)
			}
		})
	}
	for _, input := range []string{"1.2", "1.2.256", "1.x.3", "1.2.3.4"} {
		if _, err := parseRealityClientVersion(input); err == nil {
			t.Errorf("parseRealityClientVersion(%q) unexpectedly succeeded", input)
		}
	}
}

func TestRealityClientHelloWireVersionAndHybridOrder(t *testing.T) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes())
	for _, tc := range []struct {
		name, version string
		want          [3]byte
	}{
		{"default", "", [3]byte{26, 3, 27}},
		{"custom", "1.8.1", [3]byte{1, 8, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := NewRealityClient(context.Background(), logger.NOP(), "example.com", option.OutboundTLSOptions{
				ServerName: "example.com", UTLS: &option.OutboundUTLSOptions{Enabled: true, Fingerprint: "chrome"},
				Reality: &option.OutboundRealityOptions{Enabled: true, PublicKey: publicKey, ShortID: "4d04", ClientVersion: tc.version},
			})
			if err != nil {
				t.Fatal(err)
			}
			hello := captureRealityHello(t, cfg.(ConfigCompat))
			// Handshake header, legacy_version and random precede session ID.
			random := hello[6:38]
			sessionOffset := 39
			if hello[38] != 32 {
				t.Fatal("REALITY session ID must contain 32 bytes")
			}
			ciphertext := append([]byte(nil), hello[sessionOffset:sessionOffset+32]...)
			cursor := sessionOffset + 32
			take := func(n int) []byte {
				t.Helper()
				if n < 0 || cursor+n > len(hello) {
					t.Fatal("truncated ClientHello vector")
				}
				value := hello[cursor : cursor+n]
				cursor += n
				return value
			}
			vector16 := func() []byte { return take(int(binary.BigEndian.Uint16(take(2)))) }
			_ = vector16()            // cipher suites
			_ = take(int(take(1)[0])) // compression methods
			extensions := vector16()
			if cursor != len(hello) {
				t.Fatal("trailing ClientHello data")
			}
			var shares []byte
			for len(extensions) > 0 {
				if len(extensions) < 4 {
					t.Fatal("truncated extension header")
				}
				kind, size := binary.BigEndian.Uint16(extensions), int(binary.BigEndian.Uint16(extensions[2:]))
				extensions = extensions[4:]
				if size > len(extensions) {
					t.Fatal("truncated extension")
				}
				if kind == 51 {
					if shares != nil {
						t.Fatal("duplicate key_share extension")
					}
					shares = extensions[:size]
				}
				extensions = extensions[size:]
			}
			if len(shares) < 2 || int(binary.BigEndian.Uint16(shares)) != len(shares)-2 {
				t.Fatal("invalid key_share vector")
			}
			shares = shares[2:]
			var plainPublic []byte
			firstReal := true
			for len(shares) > 0 {
				if len(shares) < 4 {
					t.Fatal("truncated key share")
				}
				group, size := binary.BigEndian.Uint16(shares), int(binary.BigEndian.Uint16(shares[2:]))
				shares = shares[4:]
				if size > len(shares) {
					t.Fatal("truncated key share data")
				}
				data := shares[:size]
				shares = shares[size:]
				grease := group&0x0f0f == 0x0a0a && byte(group>>8) == byte(group)
				if !grease && firstReal {
					if group != 0x11ec || len(data) != 1184+32 {
						t.Fatal("first real share is not X25519MLKEM768")
					}
					firstReal = false
				}
				if group == 29 {
					if plainPublic != nil || len(data) != 32 {
						t.Fatal("invalid plain X25519 share")
					}
					plainPublic = data
				}
			}
			if firstReal || plainPublic == nil {
				t.Fatal("missing hybrid or plain X25519 share")
			}
			peer, err := ecdh.X25519().NewPublicKey(plainPublic)
			if err != nil {
				t.Fatal(err)
			}
			secret, err := priv.ECDH(peer)
			if err != nil {
				t.Fatal(err)
			}
			authKey := make([]byte, 32)
			if _, err := io.ReadFull(hkdf.New(sha256.New, secret, random[:20], []byte("REALITY")), authKey); err != nil {
				t.Fatal(err)
			}
			block, err := aes.NewCipher(authKey)
			if err != nil {
				t.Fatal(err)
			}
			aead, err := cipher.NewGCM(block)
			if err != nil {
				t.Fatal(err)
			}
			aad := append([]byte(nil), hello...)
			clear(aad[sessionOffset : sessionOffset+32])
			payload, err := aead.Open(nil, random[20:], ciphertext, aad)
			if err != nil {
				t.Fatal("server could not authenticate captured REALITY session")
			}
			if len(payload) != 16 || !bytes.Equal(payload[:3], tc.want[:]) {
				t.Fatal("incorrect authenticated client version")
			}
			if payload[3] != 0 {
				t.Fatal("unexpected reserved version byte")
			}
			if !bytes.Equal(payload[8:], []byte{0x4d, 0x04, 0, 0, 0, 0, 0, 0}) {
				t.Fatal("incorrect authenticated short ID")
			}
			stamp := time.Unix(int64(binary.BigEndian.Uint32(payload[4:8])), 0)
			if delta := time.Since(stamp); delta < -time.Second || delta > 5*time.Second {
				t.Fatal("incorrect authenticated timestamp")
			}
		})
	}
}

func captureRealityHello(t *testing.T, cfg ConfigCompat) []byte {
	t.Helper()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	deadline := time.Now().Add(3 * time.Second)
	_ = client.SetDeadline(deadline)
	_ = server.SetDeadline(deadline)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = cfg.ClientHandshake(ctx, client) }()
	defer func() { _ = server.Close(); _ = client.Close(); <-done }()
	header := make([]byte, 5)
	if _, err := io.ReadFull(server, header); err != nil {
		t.Fatal("read TLS record header:", err)
	}
	size := int(binary.BigEndian.Uint16(header[3:]))
	if header[0] != 22 || size < 71 || size > 16384 {
		t.Fatal("invalid ClientHello record bounds")
	}
	hello := make([]byte, size)
	if _, err := io.ReadFull(server, hello); err != nil {
		t.Fatal("read ClientHello:", err)
	}
	handshakeSize := int(hello[1])<<16 | int(hello[2])<<8 | int(hello[3])
	if hello[0] != 1 || handshakeSize != len(hello)-4 {
		t.Fatal("invalid ClientHello handshake length")
	}
	return hello
}

func TestRealityMaskCertificateFailsPromptly(t *testing.T) {
	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, private.Public(), private)
	if err != nil {
		t.Fatal(err)
	}
	realityKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := NewRealityClient(context.Background(), logger.NOP(), "example.com", option.OutboundTLSOptions{
		ServerName: "example.com", UTLS: &option.OutboundUTLSOptions{Enabled: true, Fingerprint: "chrome"},
		Reality: &option.OutboundRealityOptions{Enabled: true, PublicKey: base64.RawURLEncoding.EncodeToString(realityKey.PublicKey().Bytes())},
	})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	server, err := listener.Accept()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	defer client.Close()
	defer server.Close()
	deadline := time.Now().Add(3 * time.Second)
	_ = client.SetDeadline(deadline)
	_ = server.SetDeadline(deadline)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn := tls.Server(server, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: private}}, MinVersion: tls.VersionTLS13})
		_ = conn.Handshake()
	}()
	defer func() { _ = client.Close(); _ = server.Close(); <-done }()
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	conn, err := cfg.(ConfigCompat).ClientHandshake(ctx, client)
	if conn != nil {
		conn.Close()
		t.Fatal("mask certificate authenticated as REALITY")
	}
	var rejection *RealityRejectionError
	if !errors.As(err, &rejection) {
		t.Fatalf("want typed REALITY rejection, got %T: %v", err, err)
	}
	if ctx.Err() != nil {
		t.Fatal("rejection waited for deadline")
	}
}
