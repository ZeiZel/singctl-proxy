package license

import (
	"crypto/ed25519"
	"errors"
)

// PublicKeyB64 is the license-signing PUBLIC key, base64 (standard) encoded. It
// is injected at build time:
//
//	go build -ldflags "-X singctl/internal/license.PublicKeyB64=<base64-pubkey>"
//
// Generate the keypair with `server keygen`; keep the PRIVATE key on the license
// server only. The public key is safe to embed/commit. Empty in plain dev builds
// → EmbeddedPublicKey errors, so no license can verify until a release build sets
// it. (The unlicensed build tag bypasses verification entirely — see gate_*.go.)
var PublicKeyB64 string

// EmbeddedPublicKey decodes the build-time public key, or errors if unset.
func EmbeddedPublicKey() (ed25519.PublicKey, error) {
	if PublicKeyB64 == "" {
		return nil, errors.New("license: бинарь собран без вшитого публичного ключа лицензии (release-сборка должна задать -ldflags PublicKeyB64)")
	}
	return DecodePublic(PublicKeyB64)
}
