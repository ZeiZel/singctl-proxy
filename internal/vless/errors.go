package vless

import (
	"errors"
	"fmt"
)

// Sentinel errors returned (wrapped in *ParseError) by ParseLink. Callers match
// them with errors.Is.
var (
	ErrNotVLESS              = errors.New("not a vless:// link")
	ErrMissingUUID           = errors.New("missing UUID")
	ErrInvalidUUID           = errors.New("invalid UUID")
	ErrMissingHost           = errors.New("missing host")
	ErrMissingPort           = errors.New("missing port")
	ErrInvalidPort           = errors.New("invalid port")
	ErrMissingRealityKey     = errors.New("reality selected but public key (pbk) is missing")
	ErrUnsupportedTransport  = errors.New("unsupported transport type")
	ErrUnsupportedSecurity   = errors.New("unsupported security type")
	ErrUnsupportedEncryption = errors.New("unsupported encryption (only 'none' is supported)")
)

// ParseError annotates a sentinel error with the offending field/value.
type ParseError struct {
	Field string
	Value string
	Err   error
}

func (e *ParseError) Error() string {
	if e.Value != "" {
		return fmt.Sprintf("vless: %s=%q: %v", e.Field, e.Value, e.Err)
	}
	return fmt.Sprintf("vless: %s: %v", e.Field, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

func parseErr(field, value string, err error) *ParseError {
	return &ParseError{Field: field, Value: value, Err: err}
}
