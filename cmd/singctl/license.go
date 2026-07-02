package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"singctl/internal/license"
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

// runLicenseInstall validates a token (or file) and saves it. Returns an exit code.
func runLicenseInstall(arg string) int {
	token := strings.TrimSpace(arg)
	if data, err := os.ReadFile(arg); err == nil { // arg is a path
		token = strings.TrimSpace(string(data))
	}
	if _, err := license.Check(token, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "error: лицензия недействительна:", err)
		return 1
	}
	store := newLicenseStore()
	if store == nil {
		fmt.Fprintln(os.Stderr, "error: не удалось определить каталог конфигурации")
		return 1
	}
	if err := store.SaveLicense(token); err != nil {
		fmt.Fprintln(os.Stderr, "error: не удалось сохранить лицензию:", err)
		return 1
	}
	fmt.Println("Лицензия установлена.")
	return 0
}

// runLicenseStatus prints the current license state. Returns an exit code.
func runLicenseStatus() int {
	if !license.Enabled() {
		fmt.Println("Сборка без проверки лицензии (unlicensed).")
		return 0
	}
	claims, err := license.Check(loadLicenseToken(), time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "Лицензия:", err)
		return 1
	}
	fmt.Printf("Лицензия активна: %s (id %s)\n", claims.Subject, claims.ID)
	if claims.ExpiresAt != 0 {
		fmt.Println("Действует до:", time.Unix(claims.ExpiresAt, 0).Format("2006-01-02"))
	}
	if base := license.ServerURL(); base != "" {
		if revoked, _ := license.CheckRevoked(context.Background(), base, claims.ID); revoked {
			fmt.Fprintln(os.Stderr, "ВНИМАНИЕ: лицензия отозвана сервером")
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
		fmt.Fprintln(os.Stderr, "⚠ singctl: UNLICENSED BUILD — проверка лицензии отключена")
		return nil
	}
	claims, err := license.Check(loadLicenseToken(), time.Now())
	if err != nil {
		return fmt.Errorf("%w\nАктивируйте лицензию: sudo singctl --license <токен>", err)
	}

	base := license.ServerURL()
	if base == "" {
		fmt.Fprintln(os.Stderr, "⚠ singctl: сервер лицензий не настроен — активация и ежедневная сверка отключены")
		return nil
	}

	store := newLicenseStore()
	var state profile.LicenseState
	if store != nil {
		state, _ = store.LoadLicenseState() // missing file → zero value (never activated)
	}

	dec := decideLicenseNow(store, base, claims.ID, state)
	if dec.Warn != "" {
		fmt.Fprintln(os.Stderr, "⚠", dec.Warn)
	}
	if !dec.Allow {
		return errors.New(dec.Reason)
	}
	return nil
}

// decideLicenseNow does one online status round-trip and runs the shared
// license.DecideEnforcement decision table, persisting the resulting state
// (best-effort — a missing config dir just means state can't be remembered
// across runs, so activation is required every time). Shared by enforceLicense
// and licenseRefreshLoop's daily tick.
func decideLicenseNow(store *profile.Store, base, id string, state profile.LicenseState) license.Decision {
	ctx, cancel := context.WithTimeout(context.Background(), licenseFetchTimeout)
	status, ferr := license.FetchStatus(ctx, base, id)
	cancel()

	dec := license.DecideEnforcement(state, status, ferr, time.Now())
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
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var state profile.LicenseState
			if store != nil {
				state, _ = store.LoadLicenseState()
			}
			dec := decideLicenseNow(store, base, id, state)
			if dec.Warn != "" {
				fmt.Fprintln(os.Stderr, "⚠", dec.Warn)
			}
			if !dec.Allow {
				fmt.Fprintln(os.Stderr, "error: лицензия недействительна —", dec.Reason, "— остановка singctl")
				cancel()
				return
			}
		}
	}
}
