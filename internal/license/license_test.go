package license

import (
	"crypto/ed25519"
	"testing"
	"time"
)

func mustKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("GenerateKeypair: %v", err)
	}
	return pub, priv
}

func TestSignVerify_RoundTrip(t *testing.T) {
	pub, priv := mustKeypair(t)
	now := time.Unix(1_700_000_000, 0)

	claims, err := NewClaims("alice@example.com", now, 30*24*time.Hour, "pro")
	if err != nil {
		t.Fatalf("NewClaims: %v", err)
	}
	token, err := Sign(claims, priv)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	got, err := Verify(token, pub, now)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.Subject != "alice@example.com" || got.ID != claims.ID || len(got.Features) != 1 || got.Features[0] != "pro" {
		t.Errorf("claims round-trip mismatch: %+v", got)
	}
	if id, err := PeekID(token); err != nil || id != claims.ID {
		t.Errorf("PeekID = %q, %v; want %q", id, err, claims.ID)
	}
}

func TestVerify_Expired(t *testing.T) {
	pub, priv := mustKeypair(t)
	issued := time.Unix(1_700_000_000, 0)
	claims, _ := NewClaims("bob", issued, time.Hour)
	token, _ := Sign(claims, priv)

	if _, err := Verify(token, pub, issued.Add(2*time.Hour)); err != ErrExpired {
		t.Errorf("expected ErrExpired, got %v", err)
	}
	// Still valid within the window.
	if _, err := Verify(token, pub, issued.Add(30*time.Minute)); err != nil {
		t.Errorf("unexpected error inside validity window: %v", err)
	}
}

func TestVerify_PerpetualNeverExpires(t *testing.T) {
	pub, priv := mustKeypair(t)
	claims, _ := NewClaims("carol", time.Unix(1, 0), 0) // ttl<=0 → perpetual
	token, _ := Sign(claims, priv)
	if _, err := Verify(token, pub, time.Unix(9_000_000_000, 0)); err != nil {
		t.Errorf("perpetual license should not expire: %v", err)
	}
}

func TestVerify_WrongKeyRejected(t *testing.T) {
	_, priv := mustKeypair(t)
	otherPub, _ := mustKeypair(t)
	claims, _ := NewClaims("dave", time.Unix(1, 0), time.Hour)
	token, _ := Sign(claims, priv)
	if _, err := Verify(token, otherPub, time.Unix(2, 0)); err == nil {
		t.Error("verification with the wrong public key must fail")
	}
}

func TestVerify_TamperedPayloadRejected(t *testing.T) {
	pub, priv := mustKeypair(t)
	claims, _ := NewClaims("erin", time.Unix(1, 0), time.Hour)
	token, _ := Sign(claims, priv)

	// Flip a character in the payload section → signature must not verify.
	tampered := []byte(token)
	idx := len(tokenPrefix) + 2
	if tampered[idx] == 'A' {
		tampered[idx] = 'B'
	} else {
		tampered[idx] = 'A'
	}
	if _, err := Verify(string(tampered), pub, time.Unix(2, 0)); err == nil {
		t.Error("tampered payload must fail verification")
	}
}

func TestVerify_BadFormat(t *testing.T) {
	pub, _ := mustKeypair(t)
	for _, tok := range []string{"", "garbage", "SINGCTL-LIC.v1.onlyonepart", "SINGCTL-LIC.v2.a.b"} {
		if _, err := Verify(tok, pub, time.Now()); err == nil {
			t.Errorf("expected error for malformed token %q", tok)
		}
	}
}

func TestKeyEncodeDecode(t *testing.T) {
	pub, priv := mustKeypair(t)
	gotPub, err := DecodePublic(EncodePublic(pub))
	if err != nil || !gotPub.Equal(pub) {
		t.Errorf("public key round-trip failed: %v", err)
	}
	gotPriv, err := DecodePrivate(EncodePrivate(priv))
	if err != nil || !gotPriv.Equal(priv) {
		t.Errorf("private key round-trip failed: %v", err)
	}
	if _, err := DecodePublic("not-base64!!"); err == nil {
		t.Error("expected error decoding garbage public key")
	}
	if _, err := DecodePublic(EncodePublic(pub[:10])); err == nil {
		t.Error("expected size error for short public key")
	}
}

func TestEmbeddedPublicKey(t *testing.T) {
	old := PublicKeyB64
	defer func() { PublicKeyB64 = old }()

	PublicKeyB64 = ""
	if _, err := EmbeddedPublicKey(); err == nil {
		t.Error("empty PublicKeyB64 must error")
	}
	pub, _ := mustKeypair(t)
	PublicKeyB64 = EncodePublic(pub)
	got, err := EmbeddedPublicKey()
	if err != nil || !got.Equal(pub) {
		t.Errorf("EmbeddedPublicKey round-trip failed: %v", err)
	}
}
