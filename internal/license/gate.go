package license

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ErrNoLicense means no license token is installed. Defined here (no build tag)
// so it exists in both licensed and unlicensed builds for messaging.
var ErrNoLicense = errors.New("license: лицензия не установлена — активируйте: singctl --license <токен>")

// ErrRevoked means the license server reported this license id as revoked.
var ErrRevoked = errors.New("license: лицензия отозвана")

// statusResponse mirrors the server's GET /v1/status and POST /v1/activate
// payloads: both respond with just {"status": "..."}.
type statusResponse struct {
	Status string `json:"status"` // "active" | "revoked" | "expired" | "unknown" | "superseded"
}

// Status is a license's lifecycle state as reported by the license server.
type Status string

const (
	StatusActive  Status = "active"
	StatusRevoked Status = "revoked"
	StatusExpired Status = "expired"
	StatusUnknown Status = "unknown" // server has no record of this id
	// StatusSuperseded means this device was replaced by a newer activation of
	// the same license id elsewhere (device binding is last-wins server-side).
	StatusSuperseded Status = "superseded"
)

// FetchStatus asks the license server for id's current status, optionally
// scoped to deviceID (the &device= query param, matching this install's
// activation) so the server can report StatusSuperseded when a different
// device has since taken over the license. deviceID may be "" to fall back to
// the old id-only diagnostic behavior (no device-binding check).
//
// Unlike CheckRevoked, this is NOT fail-open: any network error, non-200
// response, or unparsable/unrecognized body returns ("", err), so callers that
// need to tell "the server said X" apart from "we couldn't reach the server"
// (e.g. the activation gate) can do so. ctx controls the request's
// deadline/cancellation; callers wanting a bounded call should wrap it with
// context.WithTimeout.
func FetchStatus(ctx context.Context, baseURL, id, deviceID string) (Status, error) {
	if baseURL == "" {
		return "", errors.New("license: empty server URL")
	}
	if id == "" {
		return "", errors.New("license: empty license id")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	u.Path = "/v1/status"
	q := url.Values{"id": {id}}
	if deviceID != "" {
		q.Set("device", deviceID)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("license: status endpoint returned %d", resp.StatusCode)
	}
	var sr statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", err
	}
	switch Status(sr.Status) {
	case StatusActive, StatusRevoked, StatusExpired, StatusUnknown, StatusSuperseded:
		return Status(sr.Status), nil
	default:
		return "", fmt.Errorf("license: unrecognized status %q", sr.Status)
	}
}

// activateRequest is the body POSTed to /v1/activate.
type activateRequest struct {
	ID       string `json:"id"`
	DeviceID string `json:"device_id"`
	Email    string `json:"email"`
}

// Activate binds id to deviceID on the license server (POST /v1/activate),
// registering email as the activating contact. The server rebinds the device
// last-wins: activating the same id from a different device supersedes the
// previous one. Fail-closed like FetchStatus: any network error, non-200
// response, or unparsable/unrecognized body returns ("", err) rather than
// guessing a status.
func Activate(ctx context.Context, baseURL, id, deviceID, email string) (Status, error) {
	if baseURL == "" {
		return "", errors.New("license: empty server URL")
	}
	if id == "" {
		return "", errors.New("license: empty license id")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	u.Path = "/v1/activate"

	body, err := json.Marshal(activateRequest{ID: id, DeviceID: deviceID, Email: email})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("license: activate endpoint returned %d", resp.StatusCode)
	}
	var sr statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", err
	}
	switch Status(sr.Status) {
	case StatusActive, StatusRevoked, StatusExpired, StatusUnknown:
		return Status(sr.Status), nil
	default:
		return "", fmt.Errorf("license: unrecognized status %q", sr.Status)
	}
}

// CheckRevoked best-effort asks the license server whether id is revoked. It is
// deliberately fail-OPEN: any network/availability/parse failure returns
// (false, err) so an offline machine or a down server never bricks a paid user.
// Only a definitive "revoked" returns (true, nil). Callers treat (true) as fatal
// and everything else as "proceed". Built on top of FetchStatus, which is
// itself fail-CLOSED (returns an error instead of swallowing it) — the
// fail-open behavior lives here, at the call site that wants it. No device id
// is sent (diagnostic-only use, e.g. `singctl --license-status`), so a
// superseded device is not distinguished from active here.
func CheckRevoked(ctx context.Context, baseURL, id string) (bool, error) {
	if baseURL == "" || id == "" {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	status, err := FetchStatus(ctx, baseURL, id, "")
	if err != nil {
		return false, err // fail-open
	}
	return status == StatusRevoked, nil
}
