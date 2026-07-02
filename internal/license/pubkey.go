package license

import (
	"crypto/ed25519"
	"errors"
	"os"
	"strings"
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

// LicenseServerDefault is the baked-in license/revocation server base URL,
// injected at build time the same way as PublicKeyB64:
//
//	go build -ldflags "-X singctl/internal/license.LicenseServerDefault=https://license.example.com"
//
// so a normal release binary can reach the server without any env var or config
// file. Empty in plain dev builds; ServerURL then returns "" unless
// SINGCTL_LICENSE_SERVER is set, which callers treat as "no server configured".
var LicenseServerDefault string

// envLicenseServer overrides LicenseServerDefault, e.g. for headless/CI runs or
// pointing a build at a staging server without recompiling.
const envLicenseServer = "SINGCTL_LICENSE_SERVER"

// ServerURL returns the license server base URL to use: the SINGCTL_LICENSE_SERVER
// env var if set (trimmed), else the build-time LicenseServerDefault. Returns ""
// when neither is set, meaning no license server is configured at all.
func ServerURL() string {
	if v := strings.TrimSpace(os.Getenv(envLicenseServer)); v != "" {
		return v
	}
	return strings.TrimSpace(LicenseServerDefault)
}

// EmbeddedPublicKey decodes the build-time public key, or errors if unset.
func EmbeddedPublicKey() (ed25519.PublicKey, error) {
	if PublicKeyB64 == "" {
		return nil, errors.New("license: бинарь собран без вшитого публичного ключа лицензии (release-сборка должна задать -ldflags PublicKeyB64)")
	}
	return DecodePublic(PublicKeyB64)
}
