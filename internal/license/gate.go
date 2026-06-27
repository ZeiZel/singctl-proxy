package license

import (
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

// statusResponse mirrors the server's GET /v1/status?id= payload.
type statusResponse struct {
	Status string `json:"status"` // "active" | "revoked" | "expired" | "unknown"
}

// CheckRevoked best-effort asks the license server whether id is revoked. It is
// deliberately fail-OPEN: any network/availability/parse failure returns
// (false, err) so an offline machine or a down server never bricks a paid user.
// Only a definitive "revoked" returns (true, nil). Callers treat (true) as fatal
// and everything else as "proceed".
func CheckRevoked(ctx context.Context, baseURL, id string) (bool, error) {
	if baseURL == "" || id == "" {
		return false, nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return false, err
	}
	u.Path = "/v1/status"
	u.RawQuery = url.Values{"id": {id}}.Encode()

	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err // fail-open
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("license: status endpoint returned %d", resp.StatusCode)
	}
	var sr statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return false, err
	}
	return sr.Status == "revoked", nil
}
