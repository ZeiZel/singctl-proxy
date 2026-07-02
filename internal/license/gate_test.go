package license

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheck_Licensed(t *testing.T) {
	if !Enabled() {
		t.Skip("built with -tags unlicensed")
	}
	pub, priv := mustKeypair(t)
	old := PublicKeyB64
	defer func() { PublicKeyB64 = old }()
	PublicKeyB64 = EncodePublic(pub)

	now := time.Unix(1_700_000_000, 0)
	claims, _ := NewClaims("user", now, time.Hour)
	token, _ := Sign(claims, priv)

	if _, err := Check(token, now); err != nil {
		t.Errorf("valid token should pass: %v", err)
	}
	if _, err := Check("", now); err != ErrNoLicense {
		t.Errorf("empty token: got %v, want ErrNoLicense", err)
	}
	if _, err := Check(token, now.Add(2*time.Hour)); err != ErrExpired {
		t.Errorf("expired token: got %v, want ErrExpired", err)
	}
}

func statusTestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("id") {
		case "revoked-id":
			_, _ = w.Write([]byte(`{"status":"revoked"}`))
		case "expired-id":
			_, _ = w.Write([]byte(`{"status":"expired"}`))
		case "unknown-id":
			_, _ = w.Write([]byte(`{"status":"unknown"}`))
		default:
			_, _ = w.Write([]byte(`{"status":"active"}`))
		}
	}))
}

func TestCheckRevoked(t *testing.T) {
	srv := statusTestServer()
	defer srv.Close()

	if revoked, err := CheckRevoked(context.Background(), srv.URL, "revoked-id"); err != nil || !revoked {
		t.Errorf("revoked-id: got (%v,%v), want (true,nil)", revoked, err)
	}
	if revoked, err := CheckRevoked(context.Background(), srv.URL, "ok-id"); err != nil || revoked {
		t.Errorf("ok-id: got (%v,%v), want (false,nil)", revoked, err)
	}
	// Unreachable server → fail-open (false, err), never a panic.
	if revoked, _ := CheckRevoked(context.Background(), "http://127.0.0.1:1", "x"); revoked {
		t.Error("unreachable server must not report revoked (fail-open)")
	}
	// No baseURL → no-op.
	if revoked, err := CheckRevoked(context.Background(), "", "x"); err != nil || revoked {
		t.Errorf("empty baseURL: got (%v,%v), want (false,nil)", revoked, err)
	}
}

func TestFetchStatus(t *testing.T) {
	srv := statusTestServer()
	defer srv.Close()

	cases := []struct {
		id   string
		want Status
	}{
		{"ok-id", StatusActive},
		{"revoked-id", StatusRevoked},
		{"expired-id", StatusExpired},
		{"unknown-id", StatusUnknown},
	}
	for _, tc := range cases {
		if got, err := FetchStatus(context.Background(), srv.URL, tc.id); err != nil || got != tc.want {
			t.Errorf("%s: got (%v,%v), want (%v,nil)", tc.id, got, err, tc.want)
		}
	}

	// Unreachable server → fail-CLOSED: an error, not a guessed status.
	if status, err := FetchStatus(context.Background(), "http://127.0.0.1:1", "x"); err == nil {
		t.Errorf("unreachable server should error, got status=%q", status)
	}
	// Empty baseURL / id → error, no request attempted.
	if _, err := FetchStatus(context.Background(), "", "x"); err == nil {
		t.Error("empty baseURL should error")
	}
	if _, err := FetchStatus(context.Background(), srv.URL, ""); err == nil {
		t.Error("empty id should error")
	}
}
