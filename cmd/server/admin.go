package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
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

// runIssue issues one or more licenses locally on the host (manual issuance
// path). It writes each record into the same JSON store the server uses and
// prints one token per line to stdout (pipeable). --subject is optional:
// pre-generated, unassigned keys are issued with a blank subject and bound to
// a device (and optionally an email) later via /v1/activate.
func runIssue(args []string) error {
	fs := flag.NewFlagSet("issue", flag.ContinueOnError)
	subject := fs.String("subject", "", "who the license is for (email/handle); optional")
	days := fs.Int("days", 365, "validity in days (0 or negative = perpetual)")
	featCSV := fs.String("features", "", "comma-separated feature flags")
	count := fs.Int("count", 1, "number of licenses to issue")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *count < 1 {
		return fmt.Errorf("--count must be >= 1")
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

	for i := 0; i < *count; i++ {
		now := time.Now()
		claims, err := license.NewClaims(*subject, now, ttl, features...)
		if err != nil {
			return err
		}
		token, err := license.Sign(claims, signer)
		if err != nil {
			return err
		}
		// Same Record shape as the server's issue() helper: DeviceID/ActivatedAt/Email
		// left at zero values until /v1/activate binds them.
		if err := store.Put(licensesrv.Record{
			Claims: claims, Token: token, Status: licensesrv.StatusActive, CreatedAt: now.Unix(),
		}); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "issued license", claims.ID, "for", subjectOrUnassigned(*subject))
		fmt.Println(token) // token to stdout, so it can be piped
	}
	return nil
}

func subjectOrUnassigned(subject string) string {
	if subject == "" {
		return "(unassigned)"
	}
	return subject
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

// runList lists licenses from the local store, optionally filtered to
// activated (DeviceID set) or unactivated (DeviceID empty) ones.
func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	activated := fs.Bool("activated", false, "only licenses with a bound device")
	unactivated := fs.Bool("unactivated", false, "only licenses with no bound device")
	asJSON := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *activated && *unactivated {
		return fmt.Errorf("--activated and --unactivated are mutually exclusive")
	}
	store, err := licensesrv.NewFileStore(dbPath())
	if err != nil {
		return err
	}
	recs, err := store.List()
	if err != nil {
		return err
	}
	filtered := make([]licensesrv.Record, 0, len(recs))
	for _, r := range recs {
		switch {
		case *activated && r.DeviceID == "":
			continue
		case *unactivated && r.DeviceID != "":
			continue
		}
		filtered = append(filtered, r)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(filtered)
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSUBJECT\tEMAIL\tDEVICE\tSTATUS\tCREATED")
	for _, r := range filtered {
		status := string(r.Status)
		if r.Status == licensesrv.StatusActive && r.Claims.Expired(time.Now()) {
			status = "expired"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Claims.ID, r.Claims.Subject, r.Email, shortDevice(r.DeviceID), status,
			time.Unix(r.CreatedAt, 0).UTC().Format(time.RFC3339))
	}
	return tw.Flush()
}

// shortDevice trims a device id for display; empty stays empty.
func shortDevice(id string) string {
	const n = 12
	if len(id) <= n {
		return id
	}
	return id[:n] + "…"
}

// runReset clears a license's device binding in the local store.
func runReset(args []string) error {
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	id := fs.String("id", "", "license id to reset")
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
	if err := store.ResetDevice(*id); err != nil {
		return err
	}
	fmt.Println("reset", *id)
	return nil
}
