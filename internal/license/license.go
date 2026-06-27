// Package license is the pure crypto core of singctl's offline licensing: a
// compact Ed25519-signed token the server issues and the CLI verifies offline
// with an embedded public key. No I/O lives here (storage/gate/HTTP are in
// sibling files / packages), so signing and verification are unit-tested
// directly. See cmd/server (issuer) and the gate_*.go build-tag split (client).
package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// tokenPrefix versions the wire format: SINGCTL-LIC.v1.<payload>.<sig>, both
// parts base64url (no padding). Bumping the version lets us evolve the format.
const tokenPrefix = "SINGCTL-LIC.v1."

// b64 encodes the token parts (URL-safe, unpadded → copy-paste friendly).
var b64 = base64.RawURLEncoding

// ErrExpired is returned by Verify when a license is past its ExpiresAt.
var ErrExpired = errors.New("license: срок действия лицензии истёк")

// Claims is the signed license payload. Field order is fixed so encoding/json
// output is deterministic (canonical signing bytes). JSON tags are short to keep
// tokens compact.
type Claims struct {
	ID        string   `json:"id"`             // unique license id (for revocation)
	Subject   string   `json:"sub"`            // who it is for (email / handle)
	IssuedAt  int64    `json:"iat"`            // unix seconds
	ExpiresAt int64    `json:"exp,omitempty"`  // unix seconds; 0 = perpetual
	Features  []string `json:"feat,omitempty"` // optional feature gating
	Nonce     string   `json:"nonce,omitempty"`
}

// Expired reports whether the license is past expiry at now (perpetual = never).
func (c Claims) Expired(now time.Time) bool {
	return c.ExpiresAt != 0 && now.Unix() > c.ExpiresAt
}

// NewClaims builds claims for issuance: a random ID + Nonce, IssuedAt=now, and
// ExpiresAt=now+ttl (ttl<=0 → perpetual). Used by the server's issue path.
func NewClaims(subject string, now time.Time, ttl time.Duration, features ...string) (Claims, error) {
	id, err := randomHex(16)
	if err != nil {
		return Claims{}, err
	}
	nonce, err := randomHex(8)
	if err != nil {
		return Claims{}, err
	}
	c := Claims{
		ID:       id,
		Subject:  subject,
		IssuedAt: now.Unix(),
		Features: features,
		Nonce:    nonce,
	}
	if ttl > 0 {
		c.ExpiresAt = now.Add(ttl).Unix()
	}
	return c, nil
}

// Sign serialises claims canonically and returns a signed token string.
func Sign(c Claims, priv ed25519.PrivateKey) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("license: invalid private key size")
	}
	payload, err := json.Marshal(c) // struct → deterministic field order
	if err != nil {
		return "", fmt.Errorf("license: marshal claims: %w", err)
	}
	sig := ed25519.Sign(priv, payload)
	return tokenPrefix + b64.EncodeToString(payload) + "." + b64.EncodeToString(sig), nil
}

// Verify checks the token's signature against pub and its expiry against now,
// returning the embedded claims. Pure; the authoritative client-side check.
func Verify(token string, pub ed25519.PublicKey, now time.Time) (Claims, error) {
	var c Claims
	if len(pub) != ed25519.PublicKeySize {
		return c, errors.New("license: invalid public key size")
	}
	rest, ok := strings.CutPrefix(strings.TrimSpace(token), tokenPrefix)
	if !ok {
		return c, errors.New("license: неизвестный формат токена")
	}
	payloadB64, sigB64, ok := strings.Cut(rest, ".")
	if !ok {
		return c, errors.New("license: повреждённый токен")
	}
	payload, err := b64.DecodeString(payloadB64)
	if err != nil {
		return c, fmt.Errorf("license: bad payload encoding: %w", err)
	}
	sig, err := b64.DecodeString(sigB64)
	if err != nil {
		return c, fmt.Errorf("license: bad signature encoding: %w", err)
	}
	if !ed25519.Verify(pub, payload, sig) {
		return c, errors.New("license: подпись не совпадает (поддельная или повреждённая лицензия)")
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return c, fmt.Errorf("license: bad payload json: %w", err)
	}
	if c.Expired(now) {
		return c, ErrExpired
	}
	return c, nil
}

// PeekID returns the license ID from a token WITHOUT verifying the signature.
// Used only to query the revocation endpoint (the signature is still verified by
// Verify separately); never trust other fields from this.
func PeekID(token string) (string, error) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(token), tokenPrefix)
	if !ok {
		return "", errors.New("license: неизвестный формат токена")
	}
	payloadB64, _, ok := strings.Cut(rest, ".")
	if !ok {
		return "", errors.New("license: повреждённый токен")
	}
	payload, err := b64.DecodeString(payloadB64)
	if err != nil {
		return "", err
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return "", err
	}
	return c.ID, nil
}

// --- key material ----------------------------------------------------------

// GenerateKeypair returns a fresh Ed25519 keypair (used by `server keygen`).
func GenerateKeypair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// EncodePublic / EncodePrivate render keys as standard base64 for storage and
// the -ldflags injection of the embedded public key.
func EncodePublic(pub ed25519.PublicKey) string    { return base64.StdEncoding.EncodeToString(pub) }
func EncodePrivate(priv ed25519.PrivateKey) string { return base64.StdEncoding.EncodeToString(priv) }

// DecodePublic / DecodePrivate parse base64 keys, validating their size.
func DecodePublic(s string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("license: decode public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("license: public key must be %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}

func DecodePrivate(s string) (ed25519.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("license: decode private key: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("license: private key must be %d bytes, got %d", ed25519.PrivateKeySize, len(raw))
	}
	return ed25519.PrivateKey(raw), nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
