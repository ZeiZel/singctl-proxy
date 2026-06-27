package licensesrv

import (
	"bytes"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"singctl/internal/license"
)

type testEnv struct {
	srv  *Server
	h    http.Handler
	pub  ed25519.PublicKey
	now  *time.Time
	whec string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	pub, priv, err := license.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	env := &testEnv{pub: pub, now: &now, whec: "whsec"}
	srv, err := New(Config{
		Store:      NewMemStore(),
		Signer:     priv,
		AdminToken: "admintok",
		Payment:    GenericHMAC{Secret: env.whec},
		DefaultTTL: 30 * 24 * time.Hour,
		Now:        func() time.Time { return *env.now },
	})
	if err != nil {
		t.Fatal(err)
	}
	env.srv = srv
	env.h = srv.Handler()
	return env
}

func (e *testEnv) do(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestIssueVerifyStatus(t *testing.T) {
	e := newTestEnv(t)

	rec := e.do(t, "POST", "/v1/admin/issue", "admintok", issueRequest{Subject: "alice", Days: 10})
	if rec.Code != http.StatusOK {
		t.Fatalf("issue: %d %s", rec.Code, rec.Body)
	}
	var resp issueResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	// The issued token verifies with the matching public key (what the CLI does).
	if _, err := license.Verify(resp.Token, e.pub, *e.now); err != nil {
		t.Fatalf("issued token must verify: %v", err)
	}

	// Public status: active now, expired after the window.
	if got := statusOf(t, e, resp.ID); got != "active" {
		t.Errorf("status = %q, want active", got)
	}
	*e.now = e.now.Add(11 * 24 * time.Hour)
	if got := statusOf(t, e, resp.ID); got != "expired" {
		t.Errorf("status after expiry = %q, want expired", got)
	}
}

func TestRevoke(t *testing.T) {
	e := newTestEnv(t)
	rec := e.do(t, "POST", "/v1/admin/issue", "admintok", issueRequest{Subject: "bob"})
	var resp issueResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if r := e.do(t, "POST", "/v1/admin/revoke", "admintok", revokeRequest{ID: resp.ID}); r.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", r.Code, r.Body)
	}
	if got := statusOf(t, e, resp.ID); got != "revoked" {
		t.Errorf("status = %q, want revoked", got)
	}
}

func TestAdminAuth(t *testing.T) {
	e := newTestEnv(t)
	if r := e.do(t, "POST", "/v1/admin/issue", "", issueRequest{Subject: "x"}); r.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", r.Code)
	}
	if r := e.do(t, "POST", "/v1/admin/issue", "wrong", issueRequest{Subject: "x"}); r.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d, want 401", r.Code)
	}
}

func TestWebhook(t *testing.T) {
	e := newTestEnv(t)
	body, _ := json.Marshal(genericPayload{Ref: "ord1", Subject: "carol", Amount: 500, Status: "paid"})
	sign := func(b []byte) string {
		m := hmac.New(sha256.New, []byte(e.whec))
		m.Write(b)
		return hex.EncodeToString(m.Sum(nil))
	}

	// Valid signature + paid → issues a license.
	req := httptest.NewRequest("POST", "/v1/webhook/payment", bytes.NewReader(body))
	req.Header.Set("X-Signature", sign(body))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook: %d %s", rec.Code, rec.Body)
	}
	var resp issueResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, err := license.Verify(resp.Token, e.pub, *e.now); err != nil {
		t.Errorf("webhook-issued token must verify: %v", err)
	}

	// Bad signature → 401, no issuance.
	req2 := httptest.NewRequest("POST", "/v1/webhook/payment", bytes.NewReader(body))
	req2.Header.Set("X-Signature", "deadbeef")
	rec2 := httptest.NewRecorder()
	e.h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("bad sig: %d, want 401", rec2.Code)
	}
}

func TestHealth(t *testing.T) {
	e := newTestEnv(t)
	if r := e.do(t, "GET", "/healthz", "", nil); r.Code != http.StatusOK {
		t.Errorf("health: %d", r.Code)
	}
}

func statusOf(t *testing.T, e *testEnv, id string) string {
	t.Helper()
	r := e.do(t, "GET", "/v1/status?id="+id, "", nil)
	var m map[string]string
	json.Unmarshal(r.Body.Bytes(), &m)
	return m["status"]
}
