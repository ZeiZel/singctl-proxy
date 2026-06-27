package licensesrv

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
)

// Payment is a normalised, verified payment extracted from a provider webhook.
type Payment struct {
	Ref     string // provider's order/payment id (idempotency + audit)
	Subject string // who the license is for (email/handle)
	Amount  int64  // minor units (kopeks/cents); informational
	Paid    bool   // true only when the provider confirms funds received
}

// PaymentProvider verifies an incoming webhook's authenticity and extracts the
// payment. Concrete adapters (YooKassa, crypto, Telegram) implement this; the
// default is GenericHMAC. The handler issues a license only when Paid is true.
type PaymentProvider interface {
	Name() string
	Verify(body []byte, headers http.Header) (Payment, error)
}

// GenericHMAC is the default adapter: it authenticates the webhook by comparing
// an HMAC-SHA256 of the raw body (hex) against the X-Signature header using a
// shared secret, then reads a small JSON envelope. It is a real, usable adapter
// for any backend that can sign its callbacks; provider-specific ones slot in
// behind the same interface.
type GenericHMAC struct{ Secret string }

func (GenericHMAC) Name() string { return "generic-hmac" }

type genericPayload struct {
	Ref     string `json:"ref"`
	Subject string `json:"subject"`
	Amount  int64  `json:"amount"`
	Status  string `json:"status"` // "paid" | "succeeded" | other
}

func (g GenericHMAC) Verify(body []byte, headers http.Header) (Payment, error) {
	if g.Secret == "" {
		return Payment{}, errors.New("licensesrv: webhook secret not configured")
	}
	want := headers.Get("X-Signature")
	mac := hmac.New(sha256.New, []byte(g.Secret))
	mac.Write(body)
	got := hex.EncodeToString(mac.Sum(nil))
	if want == "" || !hmac.Equal([]byte(got), []byte(want)) {
		return Payment{}, errors.New("licensesrv: webhook signature mismatch")
	}
	var p genericPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return Payment{}, err
	}
	if p.Subject == "" {
		return Payment{}, errors.New("licensesrv: webhook missing subject")
	}
	return Payment{
		Ref:     p.Ref,
		Subject: p.Subject,
		Amount:  p.Amount,
		Paid:    p.Status == "paid" || p.Status == "succeeded",
	}, nil
}
