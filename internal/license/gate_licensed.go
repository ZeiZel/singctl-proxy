//go:build !unlicensed

package license

import "time"

// Enabled reports whether license enforcement is compiled in. True in normal
// (shipping) builds; false only in the `-tags unlicensed` build (gate_unlicensed.go).
func Enabled() bool { return true }

// Check validates a stored license token offline against the embedded public key
// at now, returning its claims. It is the authoritative gate the CLI calls at
// startup. Revocation is a separate best-effort online step (CheckRevoked).
func Check(token string, now time.Time) (Claims, error) {
	pub, err := EmbeddedPublicKey()
	if err != nil {
		return Claims{}, err
	}
	if token == "" {
		return Claims{}, ErrNoLicense
	}
	return Verify(token, pub, now)
}
