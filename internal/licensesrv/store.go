// Package licensesrv is the singctl license server: it issues Ed25519-signed
// licenses (internal/license), persists them, exposes a public revocation
// endpoint the CLI polls, and an admin API for manual issuance + a payment
// webhook. It is small and dependency-light on purpose (stdlib net/http + a
// JSON file store) so it deploys as a single static binary with one data file.
package licensesrv

import (
	"errors"

	"singctl/internal/license"
)

// Status is a stored license's lifecycle state.
type Status string

const (
	StatusActive  Status = "active"
	StatusRevoked Status = "revoked"
)

// ErrNotFound is returned by Store.Get / SetStatus for an unknown id.
var ErrNotFound = errors.New("licensesrv: license not found")

// Record is one issued license as persisted by the server.
type Record struct {
	Claims      license.Claims `json:"claims"`
	Token       string         `json:"token"`
	Status      Status         `json:"status"`
	PaymentRef  string         `json:"payment_ref,omitempty"`
	CreatedAt   int64          `json:"created_at"`
	DeviceID    string         `json:"device_id,omitempty"`
	ActivatedAt int64          `json:"activated_at,omitempty"`
	Email       string         `json:"email,omitempty"`
}

// Store persists license records. Implementations: FileStore (default, JSON file)
// and MemStore (tests). Implementations must be safe for concurrent use.
type Store interface {
	Put(Record) error
	Get(id string) (Record, error)
	List() ([]Record, error)
	SetStatus(id string, s Status) error

	// BindDevice binds (or rebinds, last-wins) a license id to a device, setting
	// Email (optional) and ActivatedAt=now. Called by the public /v1/activate
	// endpoint.
	BindDevice(id, deviceID, email string) error
	// ResetDevice clears DeviceID/ActivatedAt/Email so the license can be
	// activated on a new device. Admin-only.
	ResetDevice(id string) error
}
