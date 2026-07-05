package bridge

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

// licenseFetchTimeout bounds each online status check (activation and the
// periodic re-check below), mirroring cmd/singctl/license.go.
const licenseFetchTimeout = 5 * time.Second

// LicenseInfo is the license state surfaced to the GUI. Enforced is false in
// unlicensed (development) builds, where Valid is reported true so the UI does
// not nag during development.
type LicenseInfo struct {
	Enforced  bool     `json:"enforced"`
	Valid     bool     `json:"valid"`
	Subject   string   `json:"subject"`
	ExpiresAt int64    `json:"expiresAt"` // unix seconds; 0 = perpetual
	DaysLeft  int      `json:"daysLeft"`  // -1 = perpetual
	Features  []string `json:"features"`
	Reason    string   `json:"reason"` // why it is invalid (empty when valid)
}

func licenseFilePath(dir string) string      { return filepath.Join(dir, "license") }
func licenseStateFilePath(dir string) string { return filepath.Join(dir, "license-state.json") }

func readLicenseFile(dir string) string {
	data, err := os.ReadFile(licenseFilePath(dir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// readLicenseState / writeLicenseState persist the same activation-lifecycle
// record as the CLI's profile.Store.{Load,Save}LicenseState, but read/write the
// file directly: the GUI runs unprivileged as the normal user (no sudo/chown-back
// concerns profile.Store exists for), and already manages the license file this
// way (see readLicenseFile above). Sharing the file (and its ~/.config/singctl
// directory) with the CLI/daemon keeps both sides' view of activation in sync.
func readLicenseState(dir string) profile.LicenseState {
	data, err := os.ReadFile(licenseStateFilePath(dir))
	if err != nil {
		return profile.LicenseState{}
	}
	var st profile.LicenseState
	_ = json.Unmarshal(data, &st) // bad/partial file → zero value, never fatal
	return st
}

func writeLicenseState(dir string, st profile.LicenseState) error {
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(licenseStateFilePath(dir), data, 0o600)
}

// licenseInfoIn validates the license stored under dir at the given time. Once
// the license server has ever reported "revoked"/"expired" for this install
// (persisted by ActivateLicense or the periodic refresh below), that verdict
// overrides an otherwise-valid offline signature — this is a read-only path
// with no network I/O, so it always reflects the last reachable check.
func licenseInfoIn(dir string, now time.Time) LicenseInfo {
	if !license.Enabled() {
		return LicenseInfo{Enforced: false, Valid: true, Subject: "development build", DaysLeft: -1}
	}
	claims, err := license.Check(readLicenseFile(dir), now)
	if err != nil {
		return LicenseInfo{Enforced: true, Valid: false, Reason: err.Error()}
	}
	// Default Features to a non-nil slice: a nil slice marshals to JSON null,
	// but the GUI types it as string[] and reads .length unconditionally, so a
	// token without a feat claim would crash the License page.
	features := claims.Features
	if features == nil {
		features = []string{}
	}
	info := LicenseInfo{
		Enforced:  true,
		Valid:     true,
		Subject:   claims.Subject,
		ExpiresAt: claims.ExpiresAt,
		Features:  features,
		DaysLeft:  -1,
	}
	if claims.ExpiresAt != 0 {
		info.DaysLeft = int(time.Unix(claims.ExpiresAt, 0).Sub(now).Hours() / 24)
	}
	switch license.Status(readLicenseState(dir).LastStatus) {
	case license.StatusRevoked:
		info.Valid, info.Reason = false, "license revoked — contact the vendor"
	case license.StatusExpired:
		info.Valid, info.Reason = false, "license expired"
	case license.StatusSuperseded:
		info.Valid, info.Reason = false, "the key was activated on another device. Re-activate it here."
	}
	return info
}

// activateLicenseIn validates a token offline, then — mirroring the CLI's
// "must contact the server successfully at least once" activation gate —
// calls license.Activate to bind this device (identified by platform.DeviceID)
// and email to the license id, requiring the server to confirm "active"
// before the token is accepted at all (when a server is configured;
// license.ServerURL() == "" keeps the old offline-only behavior, e.g. for
// isolated/dev deployments). Activation is last-wins server-side: doing this
// again from a different device supersedes this one (see StatusSuperseded).
// On success it writes both the token and the activation state (including
// email, so subsequent re-activation/rechecks don't need it re-entered) to
// dir; the running daemon's restart policy (KeepAlive/Restart) re-reads the
// token within seconds, so no explicit restart is needed.
func activateLicenseIn(ctx context.Context, dir, token, email string, now time.Time) error {
	if !license.Enabled() {
		return errors.New("development build: license enforcement is disabled")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("empty license token")
	}
	claims, err := license.Check(token, now)
	if err != nil {
		return fmt.Errorf("invalid license: %w", err)
	}

	if base := license.ServerURL(); base != "" {
		fctx, cancel := context.WithTimeout(ctx, licenseFetchTimeout)
		status, ferr := license.Activate(fctx, base, claims.ID, platform.DeviceID(), email)
		cancel()
		dec := license.DecideEnforcement(profile.LicenseState{}, status, ferr, now)
		dec.State.Email = strings.TrimSpace(email)
		if err := writeLicenseState(dir, dec.State); err != nil {
			return err
		}
		if !dec.Allow {
			return errors.New(dec.Reason)
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(licenseFilePath(dir), []byte(token+"\n"), 0o600)
}

// removeLicenseIn deletes the stored license and its activation state (no
// error if either is absent).
func removeLicenseIn(dir string) error {
	if err := os.Remove(licenseFilePath(dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(licenseStateFilePath(dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// refreshLicenseIn re-checks an already-activated license against the server
// and persists the result. Unreachable → leaves the state untouched (offline
// continuation), matching license.DecideEnforcement. Called at GUI startup and
// once a day thereafter (see licenseLoop in app.go); a no-op without a
// configured server or a stored token.
func refreshLicenseIn(ctx context.Context, dir string, now time.Time) {
	base := license.ServerURL()
	if base == "" {
		return
	}
	token := readLicenseFile(dir)
	if token == "" {
		return
	}
	claims, err := license.Check(token, now)
	if err != nil {
		return
	}
	state := readLicenseState(dir)
	fctx, cancel := context.WithTimeout(ctx, licenseFetchTimeout)
	status, ferr := license.FetchStatus(fctx, base, claims.ID, platform.DeviceID())
	cancel()
	dec := license.DecideEnforcement(state, status, ferr, now)
	dec.State.Email = state.Email // DecideEnforcement doesn't know about Email
	_ = writeLicenseState(dir, dec.State)
}

// --- Wails-bound methods ---

// GetLicense returns the current license state.
func (a *App) GetLicense() LicenseInfo {
	return licenseInfoIn(a.daemon.configDir, time.Now())
}

// ActivateLicense validates a pasted token and email, activates the device
// against the license server, and stores the token; the daemon applies it on
// its next (auto-)restart.
func (a *App) ActivateLicense(token, email string) error {
	return activateLicenseIn(a.appCtx(), a.daemon.configDir, token, email, time.Now())
}

// RemoveLicense deletes the stored license token.
func (a *App) RemoveLicense() error {
	return removeLicenseIn(a.daemon.configDir)
}

// appCtx returns the Wails app context, falling back to context.Background()
// if called before Startup (shouldn't happen via the bound API, but keeps
// activateLicenseIn's ctx argument from ever being nil).
func (a *App) appCtx() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}
