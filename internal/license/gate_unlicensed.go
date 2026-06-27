//go:build unlicensed

package license

import "time"

// This file is compiled ONLY for `-tags unlicensed` (make build-unlicensed). The
// shipping binary is built WITHOUT this tag, so it contains gate_licensed.go and
// none of this bypass — see the security note in the build docs. Never publish an
// unlicensed build.

// Enabled reports that license enforcement is compiled OUT in this build.
func Enabled() bool { return false }

// Check always succeeds in the unlicensed build (no verification performed).
func Check(_ string, _ time.Time) (Claims, error) {
	return Claims{Subject: "unlicensed-build", Features: []string{"unlicensed"}}, nil
}
