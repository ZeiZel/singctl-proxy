// Command server is the singctl license server and its admin CLI. Subcommands:
//
//	server serve     run the HTTP license server (env-configured)
//	server keygen    generate an Ed25519 keypair (private→server, public→CLI)
//	server issue     issue a license locally (manual, on the host)
//	server revoke    revoke a license id locally
//
// It is pure Go (no sing-box, CGO-free) and stores data in a single JSON file.
package main

import (
	"crypto/ed25519"
	"fmt"
	"os"

	"singctl/internal/license"
)

// version is stamped via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = runServe(os.Args[2:])
	case "keygen":
		err = runKeygen(os.Args[2:])
	case "issue":
		err = runIssue(os.Args[2:])
	case "revoke":
		err = runRevoke(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println("singctl-license", version)
		return
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand:", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `singctl license server

usage:
  server serve     run the HTTP license server (configured via env)
  server keygen    generate an Ed25519 keypair
  server issue     issue a license locally (--subject, --days, --features)
  server revoke    revoke a license id (--id)

env (serve):
  LICENSE_ADDR                 listen address (default :8080)
  LICENSE_DB                   path to the JSON store (default ./licenses.json)
  LICENSE_PRIVATE_KEY          base64 Ed25519 private key (or _FILE)
  LICENSE_PRIVATE_KEY_FILE     path to a file containing the base64 private key
  LICENSE_ADMIN_TOKEN          bearer token for /v1/admin/*
  LICENSE_WEBHOOK_SECRET       HMAC secret; set to enable POST /v1/webhook/payment
  LICENSE_DEFAULT_TTL_DAYS     default validity in days (default 365; 0=perpetual)
`)
}

// loadSigner reads the Ed25519 private key from LICENSE_PRIVATE_KEY or
// LICENSE_PRIVATE_KEY_FILE. The private key must never leave the server.
func loadSigner() (ed25519.PrivateKey, error) {
	if b64 := os.Getenv("LICENSE_PRIVATE_KEY"); b64 != "" {
		return license.DecodePrivate(b64)
	}
	if path := os.Getenv("LICENSE_PRIVATE_KEY_FILE"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return license.DecodePrivate(string(raw))
	}
	return nil, fmt.Errorf("set LICENSE_PRIVATE_KEY or LICENSE_PRIVATE_KEY_FILE")
}

func dbPath() string {
	if p := os.Getenv("LICENSE_DB"); p != "" {
		return p
	}
	return "licenses.json"
}
