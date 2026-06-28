package bridge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"singctl/internal/license"
)

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

func licenseFilePath(dir string) string { return filepath.Join(dir, "license") }

func readLicenseFile(dir string) string {
	data, err := os.ReadFile(licenseFilePath(dir))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// licenseInfoIn validates the license stored under dir at the given time.
func licenseInfoIn(dir string, now time.Time) LicenseInfo {
	if !license.Enabled() {
		return LicenseInfo{Enforced: false, Valid: true, Subject: "development build", DaysLeft: -1}
	}
	claims, err := license.Check(readLicenseFile(dir), now)
	if err != nil {
		return LicenseInfo{Enforced: true, Valid: false, Reason: err.Error()}
	}
	info := LicenseInfo{
		Enforced:  true,
		Valid:     true,
		Subject:   claims.Subject,
		ExpiresAt: claims.ExpiresAt,
		Features:  claims.Features,
		DaysLeft:  -1,
	}
	if claims.ExpiresAt != 0 {
		info.DaysLeft = int(time.Unix(claims.ExpiresAt, 0).Sub(now).Hours() / 24)
	}
	return info
}

// activateLicenseIn validates a token offline and writes it to dir/license. The
// running daemon's restart policy (KeepAlive / Restart) re-reads it within
// seconds, so no explicit restart is needed.
func activateLicenseIn(dir, token string, now time.Time) error {
	if !license.Enabled() {
		return errors.New("development build: license enforcement is disabled")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("empty license token")
	}
	if _, err := license.Check(token, now); err != nil {
		return fmt.Errorf("invalid license: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(licenseFilePath(dir), []byte(token+"\n"), 0o600)
}

// removeLicenseIn deletes the stored license (no error if absent).
func removeLicenseIn(dir string) error {
	if err := os.Remove(licenseFilePath(dir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// --- Wails-bound methods ---

// GetLicense returns the current license state.
func (a *App) GetLicense() LicenseInfo {
	return licenseInfoIn(a.daemon.configDir, time.Now())
}

// ActivateLicense validates a pasted token and stores it; the daemon applies it
// on its next (auto-)restart.
func (a *App) ActivateLicense(token string) error {
	return activateLicenseIn(a.daemon.configDir, token, time.Now())
}

// RemoveLicense deletes the stored license token.
func (a *App) RemoveLicense() error {
	return removeLicenseIn(a.daemon.configDir)
}
