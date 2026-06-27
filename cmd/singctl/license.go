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

// envLicenseToken / envLicenseServer let headless/CI runs provide the license
// token and the revocation-server base URL without an on-disk file.
const (
	envLicenseToken  = "SINGCTL_LICENSE"
	envLicenseServer = "SINGCTL_LICENSE_SERVER"
)

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
	if base := strings.TrimSpace(os.Getenv(envLicenseServer)); base != "" {
		if revoked, _ := license.CheckRevoked(context.Background(), base, claims.ID); revoked {
			fmt.Fprintln(os.Stderr, "ВНИМАНИЕ: лицензия отозвана сервером")
			return 1
		}
	}
	return 0
}

// enforceLicense is the startup gate: it blocks running our own cores without a
// valid license. In an unlicensed build it only warns. Revocation is best-effort
// (fail-open) so an offline machine or a down server never bricks a paid user.
func enforceLicense() error {
	if !license.Enabled() {
		fmt.Fprintln(os.Stderr, "⚠ singctl: UNLICENSED BUILD — проверка лицензии отключена")
		return nil
	}
	claims, err := license.Check(loadLicenseToken(), time.Now())
	if err != nil {
		return fmt.Errorf("%w\nАктивируйте лицензию: sudo singctl --license <токен>", err)
	}
	if base := strings.TrimSpace(os.Getenv(envLicenseServer)); base != "" {
		if revoked, _ := license.CheckRevoked(context.Background(), base, claims.ID); revoked {
			return errors.New("лицензия отозвана — обратитесь к поставщику")
		}
	}
	return nil
}
