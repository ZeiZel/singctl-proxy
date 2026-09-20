package sub

import (
	"context"
	"strings"
	"time"
)

// DefaultInterval is how often a subscription is refetched when the panel does
// not state its own profile-update-interval.
const DefaultInterval = 12 * time.Hour

// Subscription is one configured subscription plus the outcome of its last
// fetch. Links is cached deliberately: the daemon must come up with the servers
// it had last time even when the panel is unreachable at startup.
type Subscription struct {
	URL        string    `json:"url"`
	Title      string    `json:"title,omitempty"`
	AddedAt    time.Time `json:"added_at"`
	LastUpdate time.Time `json:"last_update,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
	Links      []string  `json:"links,omitempty"`
	Meta       Meta      `json:"meta,omitempty"`
}

// Interval is the refresh period for this subscription: whatever the panel
// asked for, else the default.
func (s Subscription) Interval() time.Duration {
	if s.Meta.UpdateInterval > 0 {
		return s.Meta.UpdateInterval
	}
	return DefaultInterval
}

// DueAt reports when this subscription should next be refetched.
func (s Subscription) DueAt() time.Time {
	if s.LastUpdate.IsZero() {
		return time.Time{} // never fetched — due immediately
	}
	return s.LastUpdate.Add(s.Interval())
}

// Label is the best human name for the subscription: the panel's title when it
// sent one, else the URL's host.
func (s Subscription) Label() string {
	if s.Title != "" {
		return s.Title
	}
	if _, rest, found := strings.Cut(s.URL, "://"); found {
		host, _, _ := strings.Cut(rest, "/")
		return host
	}
	return s.URL
}

// Fetcher retrieves a subscription. It is an interface so the executor can be
// tested without a network, and so the real implementation can decide how to
// reach a host the local network may be blocking.
type Fetcher interface {
	Fetch(ctx context.Context, url string) (Result, error)
}
