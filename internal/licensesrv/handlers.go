package licensesrv

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"singctl/internal/license"
)

const maxBody = 1 << 16 // 64 KiB request cap

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleStatus is the PUBLIC revocation endpoint the CLI polls. It leaks nothing
// beyond the lifecycle state of a license id. An optional ?device= param lets
// the CLI detect that its binding was superseded by activation on another
// device (last-wins); without it, behavior is unchanged (admin/diagnostic use).
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing id")
		return
	}
	device := r.URL.Query().Get("device")
	rec, err := s.cfg.Store.Get(id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "unknown"})
		return
	}
	status := string(rec.Status)
	if rec.Status == StatusActive && rec.Claims.Expired(s.cfg.Now()) {
		status = "expired"
	}
	if status == "active" && device != "" && rec.DeviceID != "" && rec.DeviceID != device {
		status = "superseded"
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

type activateRequest struct {
	ID       string `json:"id"`
	DeviceID string `json:"device_id"`
	Email    string `json:"email,omitempty"`
}

// handleActivate is the PUBLIC activation endpoint: it binds a license id to a
// device (last-wins — a later activation always displaces an earlier one). The
// CLI calls this once per device and then polls /v1/status?device= to detect a
// supersede.
func (s *Server) handleActivate(w http.ResponseWriter, r *http.Request) {
	var req activateRequest
	if err := readJSON(r, &req); err != nil || req.ID == "" || req.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "id and device_id are required")
		return
	}
	rec, err := s.cfg.Store.Get(req.ID)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "unknown"})
		return
	}
	if rec.Status == StatusRevoked {
		writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
		return
	}
	if rec.Status == StatusActive && rec.Claims.Expired(s.cfg.Now()) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "expired"})
		return
	}
	if err := s.cfg.Store.BindDevice(req.ID, req.DeviceID, req.Email); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "active"})
}

type issueRequest struct {
	Subject  string   `json:"subject,omitempty"` // optional: blank for pre-generated, unassigned keys
	Days     int      `json:"days,omitempty"`    // 0 → DefaultTTL; negative → perpetual
	Features []string `json:"features,omitempty"`
	Count    int      `json:"count,omitempty"` // 0 or 1 → single (back-compat); >1 → batch
}

type issueResponse struct {
	ID    string `json:"id"`
	Token string `json:"token"`
}

type issueBatchResponse struct {
	Licenses []issueResponse `json:"licenses"`
}

func (s *Server) handleIssue(w http.ResponseWriter, r *http.Request) {
	var req issueRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	count := req.Count
	if count <= 1 {
		rec, err := s.issue(req.Subject, s.ttlFromDays(req.Days), "", req.Features...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, issueResponse{ID: rec.Claims.ID, Token: rec.Token})
		return
	}
	out := make([]issueResponse, 0, count)
	for i := 0; i < count; i++ {
		rec, err := s.issue(req.Subject, s.ttlFromDays(req.Days), "", req.Features...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, issueResponse{ID: rec.Claims.ID, Token: rec.Token})
	}
	writeJSON(w, http.StatusOK, issueBatchResponse{Licenses: out})
}

type revokeRequest struct {
	ID string `json:"id"`
}

func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	var req revokeRequest
	if err := readJSON(r, &req); err != nil || req.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if err := s.cfg.Store.SetStatus(req.ID, StatusRevoked); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked", "id": req.ID})
}

// handleList returns issued licenses, optionally filtered by
// ?state=activated|unactivated (activated = a device has bound to it).
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	recs, err := s.cfg.Store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	switch r.URL.Query().Get("state") {
	case "activated":
		recs = filterRecords(recs, func(r Record) bool { return r.DeviceID != "" })
	case "unactivated":
		recs = filterRecords(recs, func(r Record) bool { return r.DeviceID == "" })
	}
	writeJSON(w, http.StatusOK, recs)
}

func filterRecords(in []Record, keep func(Record) bool) []Record {
	out := make([]Record, 0, len(in))
	for _, r := range in {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

type resetRequest struct {
	ID string `json:"id"`
}

// handleReset clears a license's device binding (admin-only) so it can be
// activated on a different device.
func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	var req resetRequest
	if err := readJSON(r, &req); err != nil || req.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if err := s.cfg.Store.ResetDevice(req.ID); err != nil {
		if errors.Is(err, ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"status": "unknown"})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

// handleWebhook auto-issues a license on a confirmed payment. The PaymentProvider
// authenticates the request (HMAC/signature) before we trust anything in it.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Payment == nil {
		writeError(w, http.StatusNotImplemented, "payments disabled")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body")
		return
	}
	pay, err := s.cfg.Payment.Verify(body, r.Header)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if !pay.Paid {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "ignored"})
		return
	}
	rec, err := s.issue(pay.Subject, s.cfg.DefaultTTL, pay.Ref)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, issueResponse{ID: rec.Claims.ID, Token: rec.Token})
}

// issue signs and persists a new license. Shared by the admin and webhook paths.
func (s *Server) issue(subject string, ttl time.Duration, paymentRef string, features ...string) (Record, error) {
	now := s.cfg.Now()
	claims, err := license.NewClaims(subject, now, ttl, features...)
	if err != nil {
		return Record{}, err
	}
	token, err := license.Sign(claims, s.cfg.Signer)
	if err != nil {
		return Record{}, err
	}
	rec := Record{
		Claims:     claims,
		Token:      token,
		Status:     StatusActive,
		PaymentRef: paymentRef,
		CreatedAt:  now.Unix(),
	}
	if err := s.cfg.Store.Put(rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// ttlFromDays maps the issue request's Days to a duration: 0 → DefaultTTL,
// negative → perpetual (0 duration), positive → that many days.
func (s *Server) ttlFromDays(days int) time.Duration {
	switch {
	case days == 0:
		return s.cfg.DefaultTTL
	case days < 0:
		return 0
	default:
		return time.Duration(days) * 24 * time.Hour
	}
}
