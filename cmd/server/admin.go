package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"singctl/internal/license"
	"singctl/internal/licensesrv"
)

// runKeygen generates an Ed25519 keypair: the PUBLIC key (embed in the CLI via
// `make build LICENSE_PUBKEY=...`) and the PRIVATE key (keep on the server only).
func runKeygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	pub, priv, err := license.GenerateKeypair()
	if err != nil {
		return err
	}
	fmt.Println("# PUBLIC key — embed in the CLI (safe to commit):")
	fmt.Println("LICENSE_PUBKEY=" + license.EncodePublic(pub))
	fmt.Println()
	fmt.Println("# PRIVATE key — server only, NEVER commit:")
	fmt.Println("LICENSE_PRIVATE_KEY=" + license.EncodePrivate(priv))
	return nil
}

// runIssue issues a license locally on the host (manual issuance path). It writes
// the record into the same JSON store the server uses and prints the token.
func runIssue(args []string) error {
	fs := flag.NewFlagSet("issue", flag.ContinueOnError)
	subject := fs.String("subject", "", "who the license is for (email/handle)")
	days := fs.Int("days", 365, "validity in days (0 or negative = perpetual)")
	featCSV := fs.String("features", "", "comma-separated feature flags")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *subject == "" {
		return fmt.Errorf("--subject is required")
	}
	signer, err := loadSigner()
	if err != nil {
		return err
	}
	store, err := licensesrv.NewFileStore(dbPath())
	if err != nil {
		return err
	}

	var ttl time.Duration
	if *days > 0 {
		ttl = time.Duration(*days) * 24 * time.Hour
	}
	var features []string
	if strings.TrimSpace(*featCSV) != "" {
		features = strings.Split(*featCSV, ",")
	}

	now := time.Now()
	claims, err := license.NewClaims(*subject, now, ttl, features...)
	if err != nil {
		return err
	}
	token, err := license.Sign(claims, signer)
	if err != nil {
		return err
	}
	if err := store.Put(licensesrv.Record{
		Claims: claims, Token: token, Status: licensesrv.StatusActive, CreatedAt: now.Unix(),
	}); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "issued license", claims.ID, "for", *subject)
	fmt.Println(token) // token to stdout, so it can be piped
	return nil
}

// runRevoke revokes a license id in the local store.
func runRevoke(args []string) error {
	fs := flag.NewFlagSet("revoke", flag.ContinueOnError)
	id := fs.String("id", "", "license id to revoke")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	store, err := licensesrv.NewFileStore(dbPath())
	if err != nil {
		return err
	}
	if err := store.SetStatus(*id, licensesrv.StatusRevoked); err != nil {
		return err
	}
	fmt.Println("revoked", *id)
	return nil
}
