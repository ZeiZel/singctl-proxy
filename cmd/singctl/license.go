package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"singctl/internal/license"
	"singctl/internal/platform"
	"singctl/internal/profile"
)

// envLicenseToken lets headless/CI runs provide the license token without an
// on-disk file. The server base URL is license.ServerURL() (env override or
// the build-time default).
const envLicenseToken = "SINGCTL_LICENSE"

// licenseFetchTimeout bounds each online status check (activation and the
// daily re-check) so a hung server never blocks startup/shutdown indefinitely.
const licenseFetchTimeout = 5 * time.Second

// newLicenseStore builds a profile store for the real user, or nil if no config
// dir resolves (rare; license is then read from env only).
func newLicenseStore() *profile.Store {
	dir, uid, gid := realConfigDir()
	if dir == "" {
		return nil
	}
	home := filepath.Dir(filepath.Dir(dir)) // <home>/.config/singctl → <home>
	return profile.NewStore(profile.OSFS{}, home, uid, gid)
}

// loadLicenseToken returns the installed token: the saved file, else the
// SINGCTL_LICENSE env var.
func loadLicenseToken() string {
	if s := newLicenseStore(); s != nil {
		if tok, err := s.LoadLicense(); err == nil && tok != "" {
			return tok
		}
	}
	return strings.TrimSpace(os.Getenv(envLicenseToken))
}

// runLicenseInstall validates a token (or file), activates it against the
// license server (binding this device + email — mirrors gui/bridge's
// activateLicenseIn), and saves the token + activation state. Returns an exit
// code.
func runLicenseInstall(arg, email string) int {
	token := strings.TrimSpace(arg)
	if data, err := os.ReadFile(arg); err == nil { // arg is a path
		token = strings.TrimSpace(string(data))
	}
	claims, err := license.Check(token, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: invalid license:", err)
		return 1
	}
	store := newLicenseStore()
	if store == nil {
		fmt.Fprintln(os.Stderr, "error: could not resolve the config directory")
		return 1
	}
	email = strings.TrimSpace(email)

	if base := license.ServerURL(); base != "" {
		// Mirror the CLI's/GUI's "must contact the server successfully at least
		// once" activation gate: bind this device (+ email) to the license id and
		// require the server to confirm "active" before the token is accepted.
		ctx, cancel := context.WithTimeout(context.Background(), licenseFetchTimeout)
		status, ferr := license.Activate(ctx, base, claims.ID, platform.DeviceID(), email)
		cancel()
		dec := license.DecideEnforcement(profile.LicenseState{}, status, ferr, time.Now())
		dec.State.Email = email
		if err := store.SaveLicenseState(dec.State); err != nil {
			fmt.Fprintln(os.Stderr, "error: could not save license state:", err)
			return 1
		}
		if !dec.Allow {
			fmt.Fprintln(os.Stderr, "error:", dec.Reason)
			return 1
		}
	} else {
		// No server configured: preserve a previously-captured email and defer
		// activation to the next enforceLicense call (offline-only deployments).
		state, _ := store.LoadLicenseState()
		state.ActivatedOnce = false
		if email != "" {
			state.Email = email
		}
		if err := store.SaveLicenseState(state); err != nil {
			fmt.Fprintln(os.Stderr, "error: could not save license state:", err)
			return 1
		}
	}

	if err := store.SaveLicense(token); err != nil {
		fmt.Fprintln(os.Stderr, "error: could not save the license:", err)
		return 1
	}
	fmt.Println("License installed.")
	return 0
}

// runLicenseRemove deletes the installed license and its activation state
// (mirrors gui/bridge's removeLicenseIn). Returns an exit code.
func runLicenseRemove() int {
	store := newLicenseStore()
	if store == nil {
		fmt.Fprintln(os.Stderr, "error: could not resolve the config directory")
		return 1
	}
	if err := store.RemoveLicense(); err != nil {
		fmt.Fprintln(os.Stderr, "error: could not remove the license:", err)
		return 1
	}
	fmt.Println("License removed.")
	return 0
}

// licenseStatusJSON is the machine-readable payload for `--license-status
// --json`, consumed by the Swift GUI. It mirrors gui/bridge's LicenseInfo
// fields (Dev takes the place of the inverse of Enforced: true in an
// unlicensed/development build).
type licenseStatusJSON struct {
	Valid     bool     `json:"valid"`
	Dev       bool     `json:"dev"`
	Subject   string   `json:"subject"`
	ExpiresAt int64    `json:"expiresAt"`
	DaysLeft  int      `json:"daysLeft"`
	Features  []string `json:"features"`
	Reason    string   `json:"reason"`
}

// licenseStatusInfo computes the current license state (offline signature +
// expiry check, plus the last online verdict persisted in license-state.json)
// without any network I/O, so `--license-status --json` is fast and always
// exits 0 — validity is reported in the JSON, not the exit code.
func licenseStatusInfo(now time.Time) licenseStatusJSON {
	if !license.Enabled() {
		return licenseStatusJSON{Valid: true, Dev: true, Subject: "development build", DaysLeft: -1, Features: []string{}}
	}
	claims, err := license.Check(loadLicenseToken(), now)
	if err != nil {
		return licenseStatusJSON{Valid: false, Reason: err.Error(), DaysLeft: -1, Features: []string{}}
	}
	features := claims.Features
	if features == nil {
		features = []string{}
	}
	info := licenseStatusJSON{
		Valid:     true,
		Subject:   claims.Subject,
		ExpiresAt: claims.ExpiresAt,
		Features:  features,
		DaysLeft:  -1,
	}
	if claims.ExpiresAt != 0 {
		info.DaysLeft = int(time.Unix(claims.ExpiresAt, 0).Sub(now).Hours() / 24)
	}
	if store := newLicenseStore(); store != nil {
		if state, err := store.LoadLicenseState(); err == nil {
			switch license.Status(state.LastStatus) {
			case license.StatusRevoked:
				info.Valid, info.Reason = false, "license revoked — contact the vendor"
			case license.StatusExpired:
				info.Valid, info.Reason = false, "license expired"
			case license.StatusSuperseded:
				info.Valid, info.Reason = false, "the key was activated on another device — re-activate it here"
			}
		}
	}
	return info
}

// runLicenseStatus prints the current license state, either as JSON (for the
// Swift GUI) or as human-readable text. Always exits 0 for --json (validity is
// carried in the payload); the text form keeps its original non-zero exit on
// an invalid/revoked license.
func runLicenseStatus(jsonOut bool) int {
	if jsonOut {
		data, _ := json.Marshal(licenseStatusInfo(time.Now()))
		fmt.Println(string(data))
		return 0
	}
	if !license.Enabled() {
		fmt.Println("Unlicensed build (license checking disabled).")
		return 0
	}
	claims, err := license.Check(loadLicenseToken(), time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "License:", err)
		return 1
	}
	fmt.Printf("License active: %s (id %s)\n", claims.Subject, claims.ID)
	if claims.ExpiresAt != 0 {
		fmt.Println("Valid until:", time.Unix(claims.ExpiresAt, 0).Format("2006-01-02"))
	}
	if base := license.ServerURL(); base != "" {
		if revoked, _ := license.CheckRevoked(context.Background(), base, claims.ID); revoked {
			fmt.Fprintln(os.Stderr, "WARNING: license has been revoked by the server")
			return 1
		}
	}
	return 0
}

// enforceLicense is the startup gate: it blocks running our own cores without a
// valid, activated license. In an unlicensed build it only warns.
//
// Enforcement has three layers:
//  1. Offline signature + expiry check (license.Check) — always required.
//  2. First run: the license server MUST be reached successfully and report
//     "active" (activation). This is what makes activation meaningful — a
//     token alone, copied around, is not enough without the server vouching
//     for it at least once.
//  3. Subsequent runs: the server is re-checked opportunistically (here, and
//     once per day via licenseRefreshLoop while running — see main.go), but a
//     network error never blocks: an already-activated install keeps working
//     offline indefinitely. Only a reachable server saying "revoked"/"expired"
//     blocks.
//
// No configured server (license.ServerURL() == "") disables all of this online
// behavior — only the offline signature check applies, with a warning.
func enforceLicense() error {
	if !license.Enabled() {
		fmt.Fprintln(os.Stderr, "⚠ singctl: UNLICENSED BUILD — license checking is disabled")
		return nil
	}
	claims, err := license.Check(loadLicenseToken(), time.Now())
	if err != nil {
		return fmt.Errorf("%w\nActivate a license: sudo singctl --license <token>", err)
	}

	base := license.ServerURL()
	if base == "" {
		fmt.Fprintln(os.Stderr, "⚠ singctl: no license server configured — activation and the daily re-check are disabled")
		return nil
	}

	store := newLicenseStore()
	var state profile.LicenseState
	if store != nil {
		state, _ = store.LoadLicenseState() // missing file → zero value (never activated)
	}

	dec := decideLicenseNow(store, base, claims.ID, platform.DeviceID(), state)
	if dec.Warn != "" {
		fmt.Fprintln(os.Stderr, "⚠", dec.Warn)
	}
	if !dec.Allow {
		return errors.New(dec.Reason)
	}
	return nil
}

// decideLicenseNow does one online round-trip and runs the shared
// license.DecideEnforcement decision table, persisting the resulting state
// (best-effort — a missing config dir just means state can't be remembered
// across runs, so activation is required every time). Shared by enforceLicense
// and licenseRefreshLoop's daily tick.
//
// The first time this id is seen on this install (!state.ActivatedOnce), it
// calls license.Activate — the write/binding call that registers deviceID
// (and state.Email, if captured via --license --email) as the license's
// current device. Every subsequent call is a read-only license.FetchStatus
// scoped to the same device, so the server can report StatusSuperseded if a
// different device has since activated the same id.
func decideLicenseNow(store *profile.Store, base, id, deviceID string, state profile.LicenseState) license.Decision {
	ctx, cancel := context.WithTimeout(context.Background(), licenseFetchTimeout)
	var status license.Status
	var ferr error
	if !state.ActivatedOnce {
		status, ferr = license.Activate(ctx, base, id, deviceID, state.Email)
	} else {
		status, ferr = license.FetchStatus(ctx, base, id, deviceID)
	}
	cancel()

	dec := license.DecideEnforcement(state, status, ferr, time.Now())
	// DecideEnforcement builds its returned State from scratch and doesn't know
	// about Email; carry it forward so it survives every persisted rewrite.
	dec.State.Email = state.Email
	if store != nil {
		_ = store.SaveLicenseState(dec.State)
	}
	return dec
}

// licenseRefreshLoop re-checks the license server once per day while singctl
// runs, so a revocation/expiry that happens mid-session takes effect without
// waiting for a restart. Mirrors monitor.Monitor.Run's ctx-driven ticker loop
// (internal/monitor/monitor.go), but with NO immediate tick: enforceLicense
// already did the equivalent check at startup. store may be nil (no resolvable
// config dir) — the check still runs, it just can't persist across runs.
// On a definitive revoked/expired verdict this cancels ctx for a graceful
// shutdown (the same path --stop/SIGTERM use) rather than os.Exit, so nothing
// is torn down mid-write.
func licenseRefreshLoop(ctx context.Context, store *profile.Store, base, id string, cancel context.CancelFunc) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	deviceID := platform.DeviceID()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var state profile.LicenseState
			if store != nil {
				state, _ = store.LoadLicenseState()
			}
			dec := decideLicenseNow(store, base, id, deviceID, state)
			if dec.Warn != "" {
				fmt.Fprintln(os.Stderr, "⚠", dec.Warn)
			}
			if !dec.Allow {
				fmt.Fprintln(os.Stderr, "error: license invalid —", dec.Reason, "— stopping singctl")
				cancel()
				return
			}
		}
	}
}
