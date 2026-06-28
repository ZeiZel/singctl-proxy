package bridge

import (
	"os"
	"testing"
	"time"

	"singctl/internal/license"
)

// withEmbeddedKey installs a throwaway signing keypair as the embedded public key
// for the duration of a test and returns a function that signs a token.
func withEmbeddedKey(t *testing.T) func(subject string, ttl time.Duration, now time.Time) string {
	t.Helper()
	pub, priv, err := license.GenerateKeypair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	previous := license.PublicKeyB64
	license.PublicKeyB64 = license.EncodePublic(pub)
	t.Cleanup(func() { license.PublicKeyB64 = previous })
	return func(subject string, ttl time.Duration, now time.Time) string {
		claims, err := license.NewClaims(subject, now, ttl, "pro")
		if err != nil {
			t.Fatalf("claims: %v", err)
		}
		token, err := license.Sign(claims, priv)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return token
	}
}

func TestLicense_ActivateValidateRemove(t *testing.T) {
	if !license.Enabled() {
		t.Skip("unlicensed build: license gate compiled out")
	}
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	sign := withEmbeddedKey(t)

	// No license yet → invalid with a reason.
	if info := licenseInfoIn(dir, now); info.Valid || info.Reason == "" {
		t.Fatalf("expected invalid+reason before activation, got %+v", info)
	}

	// Activate a valid token.
	token := sign("alice@example.com", 30*24*time.Hour, now)
	if err := activateLicenseIn(dir, token, now); err != nil {
		t.Fatalf("activate: %v", err)
	}
	info := licenseInfoIn(dir, now)
	if !info.Valid || info.Subject != "alice@example.com" {
		t.Fatalf("expected valid for alice, got %+v", info)
	}
	if info.DaysLeft < 29 || info.DaysLeft > 30 {
		t.Errorf("DaysLeft = %d, want ~30", info.DaysLeft)
	}

	// Remove → invalid again, file gone.
	if err := removeLicenseIn(dir); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(licenseFilePath(dir)); !os.IsNotExist(err) {
		t.Error("license file should be gone after remove")
	}
	if licenseInfoIn(dir, now).Valid {
		t.Error("license should be invalid after remove")
	}
}

func TestLicense_RejectsGarbageAndEmpty(t *testing.T) {
	if !license.Enabled() {
		t.Skip("unlicensed build")
	}
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	withEmbeddedKey(t)

	if err := activateLicenseIn(dir, "   ", now); err == nil {
		t.Error("empty token should be rejected")
	}
	if err := activateLicenseIn(dir, "SINGCTL-LIC.v1.not-a-real-token", now); err == nil {
		t.Error("garbage token should be rejected")
	}
	// removeLicenseIn is a no-op when nothing is stored.
	if err := removeLicenseIn(dir); err != nil {
		t.Errorf("remove on empty dir should be nil, got %v", err)
	}
}
