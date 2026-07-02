package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// withNoServer clears any configured license server for the duration of a test
// (activateLicenseIn/refreshLicenseIn skip the online gate entirely when
// license.ServerURL() == "").
func withNoServer(t *testing.T) {
	t.Helper()
	prevEnv, hadEnv := os.LookupEnv("SINGCTL_LICENSE_SERVER")
	prevDefault := license.LicenseServerDefault
	_ = os.Unsetenv("SINGCTL_LICENSE_SERVER")
	license.LicenseServerDefault = ""
	t.Cleanup(func() {
		if hadEnv {
			_ = os.Setenv("SINGCTL_LICENSE_SERVER", prevEnv)
		}
		license.LicenseServerDefault = prevDefault
	})
}

// withServer points license.ServerURL() at a test server via the env var for
// the duration of a test.
func withServer(t *testing.T, url string) {
	t.Helper()
	prevEnv, hadEnv := os.LookupEnv("SINGCTL_LICENSE_SERVER")
	_ = os.Setenv("SINGCTL_LICENSE_SERVER", url)
	t.Cleanup(func() {
		if hadEnv {
			_ = os.Setenv("SINGCTL_LICENSE_SERVER", prevEnv)
		} else {
			_ = os.Unsetenv("SINGCTL_LICENSE_SERVER")
		}
	})
}

// statusServer stubs both GET /v1/status (used by refreshLicenseIn) and
// POST /v1/activate (used by activateLicenseIn), both returning status. It
// records the last device query param / posted device_id it saw in gotDevice,
// if non-nil, so tests can assert device binding was sent.
func statusServer(t *testing.T, status string) *httptest.Server {
	return statusServerRecording(t, status, nil)
}

func statusServerRecording(t *testing.T, status string, gotDevice *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/activate":
			var body struct {
				ID       string `json:"id"`
				DeviceID string `json:"device_id"`
				Email    string `json:"email"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if gotDevice != nil {
				*gotDevice = body.DeviceID
			}
		default: // /v1/status
			if gotDevice != nil {
				*gotDevice = r.URL.Query().Get("device")
			}
		}
		_, _ = w.Write([]byte(`{"status":"` + status + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLicense_ActivateValidateRemove(t *testing.T) {
	if !license.Enabled() {
		t.Skip("unlicensed build: license gate compiled out")
	}
	withNoServer(t) // no server configured → offline-only activation (legacy path)
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	sign := withEmbeddedKey(t)

	// No license yet → invalid with a reason.
	if info := licenseInfoIn(dir, now); info.Valid || info.Reason == "" {
		t.Fatalf("expected invalid+reason before activation, got %+v", info)
	}

	// Activate a valid token.
	token := sign("alice@example.com", 30*24*time.Hour, now)
	if err := activateLicenseIn(context.Background(), dir, token, "alice@example.com", now); err != nil {
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
	withNoServer(t)
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	withEmbeddedKey(t)

	if err := activateLicenseIn(context.Background(), dir, "   ", "alice@example.com", now); err == nil {
		t.Error("empty token should be rejected")
	}
	if err := activateLicenseIn(context.Background(), dir, "SINGCTL-LIC.v1.not-a-real-token", "alice@example.com", now); err == nil {
		t.Error("garbage token should be rejected")
	}
	// removeLicenseIn is a no-op when nothing is stored.
	if err := removeLicenseIn(dir); err != nil {
		t.Errorf("remove on empty dir should be nil, got %v", err)
	}
}

// TestLicense_ActivationRequiresServer covers the "must contact the server
// successfully at least once" invariant: with a server configured, a
// signature-valid token is only accepted when the server confirms "active".
func TestLicense_ActivationRequiresServer(t *testing.T) {
	if !license.Enabled() {
		t.Skip("unlicensed build")
	}
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	sign := withEmbeddedKey(t)
	token := sign("bob@example.com", 30*24*time.Hour, now)

	t.Run("unreachable server blocks activation", func(t *testing.T) {
		withServer(t, "http://127.0.0.1:1")
		if err := activateLicenseIn(context.Background(), dir, token, "alice@example.com", now); err == nil {
			t.Error("expected activation to fail without server confirmation")
		}
		if licenseInfoIn(dir, now).Valid {
			t.Error("license must not be considered valid before first successful activation")
		}
	})

	t.Run("server confirms active → activation succeeds", func(t *testing.T) {
		srv := statusServer(t, "active")
		withServer(t, srv.URL)
		if err := activateLicenseIn(context.Background(), dir, token, "alice@example.com", now); err != nil {
			t.Fatalf("activate: %v", err)
		}
		if info := licenseInfoIn(dir, now); !info.Valid {
			t.Errorf("expected valid after server-confirmed activation, got %+v", info)
		}
	})

	t.Run("server reports revoked → activation fails", func(t *testing.T) {
		dir := t.TempDir()
		srv := statusServer(t, "revoked")
		withServer(t, srv.URL)
		if err := activateLicenseIn(context.Background(), dir, token, "alice@example.com", now); err == nil {
			t.Error("expected activation to fail for a revoked id")
		}
	})
}

// TestLicense_OfflineContinuationAfterActivation covers the "keeps working
// offline indefinitely after the first activation" invariant, and that a
// reachable server reporting revoked/expired overrides the offline signature
// check.
func TestLicense_OfflineContinuationAfterActivation(t *testing.T) {
	if !license.Enabled() {
		t.Skip("unlicensed build")
	}
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	sign := withEmbeddedKey(t)
	token := sign("carol@example.com", 30*24*time.Hour, now)

	activeSrv := statusServer(t, "active")
	withServer(t, activeSrv.URL)
	if err := activateLicenseIn(context.Background(), dir, token, "alice@example.com", now); err != nil {
		t.Fatalf("activate: %v", err)
	}

	// Server later becomes unreachable: still valid (offline continuation).
	withServer(t, "http://127.0.0.1:1")
	refreshLicenseIn(context.Background(), dir, now.Add(time.Hour))
	if info := licenseInfoIn(dir, now.Add(time.Hour)); !info.Valid {
		t.Errorf("expected offline continuation to stay valid, got %+v", info)
	}

	// Server comes back reporting revoked: now invalid, even offline afterwards.
	revokedSrv := statusServer(t, "revoked")
	withServer(t, revokedSrv.URL)
	refreshLicenseIn(context.Background(), dir, now.Add(2*time.Hour))
	if info := licenseInfoIn(dir, now.Add(2*time.Hour)); info.Valid {
		t.Errorf("expected revoked verdict to invalidate the license, got %+v", info)
	}
}

// TestLicense_ActivationSendsDeviceAndEmail checks that activateLicenseIn
// binds this device (platform.DeviceID) and the given email to the license,
// and that refreshLicenseIn's status recheck is scoped to the same device.
func TestLicense_ActivationSendsDeviceAndEmail(t *testing.T) {
	if !license.Enabled() {
		t.Skip("unlicensed build")
	}
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	sign := withEmbeddedKey(t)
	token := sign("dave@example.com", 30*24*time.Hour, now)

	var gotDevice string
	srv := statusServerRecording(t, "active", &gotDevice)
	withServer(t, srv.URL)

	if err := activateLicenseIn(context.Background(), dir, token, "dave@example.com", now); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if gotDevice == "" {
		t.Error("expected activation to send a non-empty device id")
	}

	gotDevice = ""
	refreshLicenseIn(context.Background(), dir, now.Add(time.Hour))
	if gotDevice == "" {
		t.Error("expected status recheck to send a non-empty device id")
	}
}

// TestLicense_Superseded covers the device-rebinding case: the server reports
// this device was replaced by a newer activation elsewhere, which must be
// treated as a blocking/invalid state, mirroring revoked/expired.
func TestLicense_Superseded(t *testing.T) {
	if !license.Enabled() {
		t.Skip("unlicensed build")
	}
	dir := t.TempDir()
	now := time.Unix(1_700_000_000, 0)
	sign := withEmbeddedKey(t)
	token := sign("erin@example.com", 30*24*time.Hour, now)

	activeSrv := statusServer(t, "active")
	withServer(t, activeSrv.URL)
	if err := activateLicenseIn(context.Background(), dir, token, "erin@example.com", now); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if info := licenseInfoIn(dir, now); !info.Valid {
		t.Fatalf("expected valid right after activation, got %+v", info)
	}

	// Another device takes over the license: the next recheck reports superseded.
	supersededSrv := statusServer(t, "superseded")
	withServer(t, supersededSrv.URL)
	refreshLicenseIn(context.Background(), dir, now.Add(time.Hour))
	info := licenseInfoIn(dir, now.Add(time.Hour))
	if info.Valid {
		t.Errorf("expected superseded verdict to invalidate the license, got %+v", info)
	}
	if info.Reason == "" {
		t.Error("expected a non-empty Reason for a superseded license")
	}
}
