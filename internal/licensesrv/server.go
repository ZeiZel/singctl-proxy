package licensesrv

import (
	"crypto/ed25519"
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"time"
)

// Config wires the server's dependencies. Signer + Store are required; the rest
// have safe defaults.
type Config struct {
	Store      Store
	Signer     ed25519.PrivateKey // license-signing private key (server-only)
	AdminToken string             // bearer token for /v1/admin/*
	Payment    PaymentProvider    // webhook adapter (nil → webhook disabled)
	DefaultTTL time.Duration      // default license validity (0 → perpetual)
	Now        func() time.Time   // injectable clock (tests)
	Logger     *log.Logger
}

// Server is the license HTTP server.
type Server struct {
	cfg Config
}

// New validates the config and returns a Server.
func New(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, errors.New("licensesrv: Store is required")
	}
	if len(cfg.Signer) != ed25519.PrivateKeySize {
		return nil, errors.New("licensesrv: a valid signing key is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	return &Server{cfg: cfg}, nil
}

// Handler returns the HTTP handler (routes + recover/logging middleware).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /v1/status", s.handleStatus)
	mux.HandleFunc("POST /v1/admin/issue", s.requireAdmin(s.handleIssue))
	mux.HandleFunc("POST /v1/admin/revoke", s.requireAdmin(s.handleRevoke))
	mux.HandleFunc("GET /v1/admin/licenses", s.requireAdmin(s.handleList))
	mux.HandleFunc("POST /v1/webhook/payment", s.handleWebhook)
	return s.recover(s.logRequests(mux))
}

// requireAdmin gates a handler behind a constant-time bearer-token check.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		got := r.Header.Get("Authorization")
		if s.cfg.AdminToken == "" || len(got) <= len(prefix) ||
			subtle.ConstantTimeCompare([]byte(got[len(prefix):]), []byte(s.cfg.AdminToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.cfg.Logger.Printf("panic: %v", v)
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.cfg.Now()
		next.ServeHTTP(w, r)
		s.cfg.Logger.Printf("%s %s %s", r.Method, r.URL.Path, s.cfg.Now().Sub(start))
	})
}
