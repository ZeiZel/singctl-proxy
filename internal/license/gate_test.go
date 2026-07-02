package license

import (
	"context"
	"encoding/json"
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
		case "superseded-id":
			_, _ = w.Write([]byte(`{"status":"superseded"}`))
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
		{"superseded-id", StatusSuperseded},
	}
	for _, tc := range cases {
		if got, err := FetchStatus(context.Background(), srv.URL, tc.id, ""); err != nil || got != tc.want {
			t.Errorf("%s: got (%v,%v), want (%v,nil)", tc.id, got, err, tc.want)
		}
		// Same behavior with a device id attached.
		if got, err := FetchStatus(context.Background(), srv.URL, tc.id, "device-1"); err != nil || got != tc.want {
			t.Errorf("%s (with device): got (%v,%v), want (%v,nil)", tc.id, got, err, tc.want)
		}
	}

	// Unreachable server → fail-CLOSED: an error, not a guessed status.
	if status, err := FetchStatus(context.Background(), "http://127.0.0.1:1", "x", ""); err == nil {
		t.Errorf("unreachable server should error, got status=%q", status)
	}
	// Empty baseURL / id → error, no request attempted.
	if _, err := FetchStatus(context.Background(), "", "x", ""); err == nil {
		t.Error("empty baseURL should error")
	}
	if _, err := FetchStatus(context.Background(), srv.URL, "", ""); err == nil {
		t.Error("empty id should error")
	}
}

// activateTestServer stubs POST /v1/activate, echoing a status keyed off id
// and recording the device id + email it was sent, for assertions.
func activateTestServer(t *testing.T) (*httptest.Server, *activateRequest) {
	t.Helper()
	var last activateRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&last); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		switch last.ID {
		case "revoked-id":
			_, _ = w.Write([]byte(`{"status":"revoked"}`))
		default:
			_, _ = w.Write([]byte(`{"status":"active"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &last
}

func TestActivate(t *testing.T) {
	srv, last := activateTestServer(t)

	status, err := Activate(context.Background(), srv.URL, "ok-id", "device-123", "alice@example.com")
	if err != nil || status != StatusActive {
		t.Fatalf("got (%v,%v), want (active,nil)", status, err)
	}
	if last.ID != "ok-id" || last.DeviceID != "device-123" || last.Email != "alice@example.com" {
		t.Errorf("server received %+v", last)
	}

	if status, err := Activate(context.Background(), srv.URL, "revoked-id", "device-123", "a@b.c"); err != nil || status != StatusRevoked {
		t.Errorf("got (%v,%v), want (revoked,nil)", status, err)
	}

	// Unreachable server → fail-CLOSED.
	if status, err := Activate(context.Background(), "http://127.0.0.1:1", "x", "d", "e"); err == nil {
		t.Errorf("unreachable server should error, got status=%q", status)
	}
	// Empty baseURL / id → error, no request attempted.
	if _, err := Activate(context.Background(), "", "x", "d", "e"); err == nil {
		t.Error("empty baseURL should error")
	}
	if _, err := Activate(context.Background(), srv.URL, "", "d", "e"); err == nil {
		t.Error("empty id should error")
	}
}
