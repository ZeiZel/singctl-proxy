package licensesrv

import (
	"encoding/json"
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
// beyond the lifecycle state of a license id.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing id")
		return
	}
	rec, err := s.cfg.Store.Get(id)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "unknown"})
		return
	}
	status := string(rec.Status)
	if rec.Status == StatusActive && rec.Claims.Expired(s.cfg.Now()) {
		status = "expired"
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

type issueRequest struct {
	Subject  string   `json:"subject"`
	Days     int      `json:"days,omitempty"` // 0 → DefaultTTL; negative → perpetual
	Features []string `json:"features,omitempty"`
}

type issueResponse struct {
	ID    string `json:"id"`
	Token string `json:"token"`
}

func (s *Server) handleIssue(w http.ResponseWriter, r *http.Request) {
	var req issueRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	if req.Subject == "" {
		writeError(w, http.StatusBadRequest, "subject is required")
		return
	}
	rec, err := s.issue(req.Subject, s.ttlFromDays(req.Days), "", req.Features...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, issueResponse{ID: rec.Claims.ID, Token: rec.Token})
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

func (s *Server) handleList(w http.ResponseWriter, _ *http.Request) {
	recs, err := s.cfg.Store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, recs)
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
