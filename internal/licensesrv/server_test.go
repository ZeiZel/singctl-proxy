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

func TestActivateBindsDeviceAndRebindLastWins(t *testing.T) {
	e := newTestEnv(t)
	rec := e.do(t, "POST", "/v1/admin/issue", "admintok", issueRequest{Subject: "alice", Days: 10})
	var resp issueResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	// First activation binds device A.
	r := e.do(t, "POST", "/v1/activate", "", activateRequest{ID: resp.ID, DeviceID: "deviceA", Email: "a@example.com"})
	if r.Code != http.StatusOK {
		t.Fatalf("activate: %d %s", r.Code, r.Body)
	}
	var m map[string]string
	json.Unmarshal(r.Body.Bytes(), &m)
	if m["status"] != "active" {
		t.Fatalf("activate status = %q, want active", m["status"])
	}

	// status?device=deviceA → active.
	if got := statusOfDevice(t, e, resp.ID, "deviceA"); got != "active" {
		t.Errorf("status(deviceA) = %q, want active", got)
	}
	// status?device=deviceB (a different device than the bound one) → superseded.
	if got := statusOfDevice(t, e, resp.ID, "deviceB"); got != "superseded" {
		t.Errorf("status(deviceB before rebind) = %q, want superseded", got)
	}

	// Rebind to device B: last-wins.
	r2 := e.do(t, "POST", "/v1/activate", "", activateRequest{ID: resp.ID, DeviceID: "deviceB"})
	if r2.Code != http.StatusOK {
		t.Fatalf("re-activate: %d %s", r2.Code, r2.Body)
	}
	var m2 map[string]string
	json.Unmarshal(r2.Body.Bytes(), &m2)
	if m2["status"] != "active" {
		t.Fatalf("re-activate status = %q, want active", m2["status"])
	}

	// Now device A is superseded, device B is active.
	if got := statusOfDevice(t, e, resp.ID, "deviceA"); got != "superseded" {
		t.Errorf("status(deviceA after rebind) = %q, want superseded", got)
	}
	if got := statusOfDevice(t, e, resp.ID, "deviceB"); got != "active" {
		t.Errorf("status(deviceB after rebind) = %q, want active", got)
	}
	// Without ?device, unchanged diagnostic behavior.
	if got := statusOf(t, e, resp.ID); got != "active" {
		t.Errorf("status(no device) = %q, want active", got)
	}
}

func TestActivateUnknownRevokedExpired(t *testing.T) {
	e := newTestEnv(t)

	if got := activateStatus(t, e, "nosuchid", "dev1"); got != "unknown" {
		t.Errorf("activate unknown id = %q, want unknown", got)
	}

	rec := e.do(t, "POST", "/v1/admin/issue", "admintok", issueRequest{Subject: "bob"})
	var resp issueResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	e.do(t, "POST", "/v1/admin/revoke", "admintok", revokeRequest{ID: resp.ID})
	if got := activateStatus(t, e, resp.ID, "dev1"); got != "revoked" {
		t.Errorf("activate revoked license = %q, want revoked", got)
	}

	rec2 := e.do(t, "POST", "/v1/admin/issue", "admintok", issueRequest{Subject: "carl", Days: 1})
	var resp2 issueResponse
	json.Unmarshal(rec2.Body.Bytes(), &resp2)
	*e.now = e.now.Add(2 * 24 * time.Hour)
	if got := activateStatus(t, e, resp2.ID, "dev1"); got != "expired" {
		t.Errorf("activate expired license = %q, want expired", got)
	}
}

func TestAdminReset(t *testing.T) {
	e := newTestEnv(t)
	rec := e.do(t, "POST", "/v1/admin/issue", "admintok", issueRequest{Subject: "dave"})
	var resp issueResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	e.do(t, "POST", "/v1/activate", "", activateRequest{ID: resp.ID, DeviceID: "devX"})
	if got := statusOfDevice(t, e, resp.ID, "devY"); got != "superseded" {
		t.Fatalf("before reset, other device = %q, want superseded", got)
	}

	r := e.do(t, "POST", "/v1/admin/reset", "admintok", resetRequest{ID: resp.ID})
	if r.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", r.Code, r.Body)
	}
	var m map[string]string
	json.Unmarshal(r.Body.Bytes(), &m)
	if m["status"] != "reset" {
		t.Errorf("reset status = %q, want reset", m["status"])
	}

	// After reset, any device is fine (no supersede) — device binding cleared.
	if got := statusOfDevice(t, e, resp.ID, "devY"); got != "active" {
		t.Errorf("after reset, status(devY) = %q, want active", got)
	}

	// Reset of an unknown id → 404.
	r2 := e.do(t, "POST", "/v1/admin/reset", "admintok", resetRequest{ID: "nosuchid"})
	if r2.Code != http.StatusNotFound {
		t.Errorf("reset unknown id: %d, want 404", r2.Code)
	}
}

func TestIssueCountBatch(t *testing.T) {
	e := newTestEnv(t)
	r := e.do(t, "POST", "/v1/admin/issue", "admintok", issueRequest{Count: 3})
	if r.Code != http.StatusOK {
		t.Fatalf("issue batch: %d %s", r.Code, r.Body)
	}
	var resp issueBatchResponse
	if err := json.Unmarshal(r.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Licenses) != 3 {
		t.Fatalf("licenses len = %d, want 3", len(resp.Licenses))
	}
	seen := map[string]bool{}
	for _, l := range resp.Licenses {
		if l.ID == "" || l.Token == "" {
			t.Errorf("empty id/token in batch: %+v", l)
		}
		if seen[l.ID] {
			t.Errorf("duplicate id in batch: %s", l.ID)
		}
		seen[l.ID] = true
		if _, err := license.Verify(l.Token, e.pub, *e.now); err != nil {
			t.Errorf("batch token must verify: %v", err)
		}
	}
}

func TestIssueSubjectOptional(t *testing.T) {
	e := newTestEnv(t)
	r := e.do(t, "POST", "/v1/admin/issue", "admintok", issueRequest{})
	if r.Code != http.StatusOK {
		t.Fatalf("issue with no subject: %d %s", r.Code, r.Body)
	}
	var resp issueResponse
	json.Unmarshal(r.Body.Bytes(), &resp)
	if resp.ID == "" || resp.Token == "" {
		t.Fatalf("expected valid id/token for subject-less issue, got %+v", resp)
	}
}

func TestListStateFilter(t *testing.T) {
	e := newTestEnv(t)
	r1 := e.do(t, "POST", "/v1/admin/issue", "admintok", issueRequest{Subject: "x"})
	var resp1 issueResponse
	json.Unmarshal(r1.Body.Bytes(), &resp1)
	r2 := e.do(t, "POST", "/v1/admin/issue", "admintok", issueRequest{Subject: "y"})
	var resp2 issueResponse
	json.Unmarshal(r2.Body.Bytes(), &resp2)

	e.do(t, "POST", "/v1/activate", "", activateRequest{ID: resp1.ID, DeviceID: "devZ"})

	activated := listState(t, e, "activated")
	if len(activated) != 1 || activated[0].Claims.ID != resp1.ID {
		t.Errorf("activated list = %+v, want just %s", activated, resp1.ID)
	}
	unactivated := listState(t, e, "unactivated")
	if len(unactivated) != 1 || unactivated[0].Claims.ID != resp2.ID {
		t.Errorf("unactivated list = %+v, want just %s", unactivated, resp2.ID)
	}
	all := listState(t, e, "")
	if len(all) != 2 {
		t.Errorf("unfiltered list len = %d, want 2", len(all))
	}
}

func listState(t *testing.T, e *testEnv, state string) []Record {
	t.Helper()
	path := "/v1/admin/licenses"
	if state != "" {
		path += "?state=" + state
	}
	r := e.do(t, "GET", path, "admintok", nil)
	var recs []Record
	json.Unmarshal(r.Body.Bytes(), &recs)
	return recs
}

func activateStatus(t *testing.T, e *testEnv, id, device string) string {
	t.Helper()
	r := e.do(t, "POST", "/v1/activate", "", activateRequest{ID: id, DeviceID: device})
	var m map[string]string
	json.Unmarshal(r.Body.Bytes(), &m)
	return m["status"]
}

func statusOfDevice(t *testing.T, e *testEnv, id, device string) string {
	t.Helper()
	r := e.do(t, "GET", "/v1/status?id="+id+"&device="+device, "", nil)
	var m map[string]string
	json.Unmarshal(r.Body.Bytes(), &m)
	return m["status"]
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
