package app

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"singctl/internal/sub"
)

// TestSubscriptionFlow_EndToEnd wires the REAL fetcher and the REAL body parser
// to a live HTTP server and drives the executor through it. The per-package
// tests each prove their own half; this proves the halves fit — a panel dialect
// the parser mishandles, or a merge that loses servers, shows up here and
// nowhere else.
func TestSubscriptionFlow_EndToEnd(t *testing.T) {
	const (
		serverA = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@a.example.com:443?security=tls&sni=a.example.com#A"
		serverB = "hysteria2://s3cr3t@b.example.com:443?sni=b.example.com&up=100&down=200#B"
		manual  = "trojan://pw@manual.example.com:443?sni=manual.example.com#manual"
	)

	// A panel serving base64 of a comma-separated list: the exact shape the
	// user described, and the one a naive comma split would corrupt.
	body := base64.StdEncoding.EncodeToString([]byte(serverA + "," + serverB))
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("profile-title", "Test Panel")
		w.Header().Set("profile-update-interval", "6")
		w.Header().Set("subscription-userinfo", "upload=100; download=200; total=1000; expire=4102444800")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	e, _ := newExecutor()
	var saved string
	e.SetSaver(func(s string) error { saved = s; return nil })
	var persisted []sub.Subscription
	e.SetSubscriptionDeps(sub.NewHTTPFetcher(nil, ""), func(s []sub.Subscription) error {
		persisted = s
		return nil
	})

	ctx := context.Background()
	if err := e.LoadLink(ctx, manual); err != nil {
		t.Fatalf("LoadLink: %v", err)
	}
	if err := e.AddSubscription(ctx, srv.URL); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}

	// Manual key keeps priority; both subscription servers follow, in order.
	got := e.CurrentLinks()
	want := []string{manual, serverA, serverB}
	if len(got) != len(want) {
		t.Fatalf("effective links = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("link %d = %q, want %q", i, got[i], want[i])
		}
	}

	// Only the manual key is persisted as a key: subscription servers belong to
	// the subscription and are re-fetched, not remembered as if hand-typed.
	if strings.TrimSpace(saved) != manual {
		t.Errorf("saved profile = %q, want just the manual key %q", saved, manual)
	}

	// The panel's metadata survives the round trip.
	if len(persisted) != 1 {
		t.Fatalf("persisted %d subscriptions, want 1", len(persisted))
	}
	rec := persisted[0]
	if rec.Title != "Test Panel" {
		t.Errorf("title = %q, want %q", rec.Title, "Test Panel")
	}
	if rec.Interval() != 6*time.Hour {
		t.Errorf("interval = %v, want 6h (from profile-update-interval)", rec.Interval())
	}
	if rec.Meta.Used() != 300 || rec.Meta.Total != 1000 {
		t.Errorf("usage = %d/%d, want 300/1000", rec.Meta.Used(), rec.Meta.Total)
	}
	if rec.Meta.Expire.IsZero() {
		t.Error("expire not parsed")
	}

	// Removing the subscription takes its servers with it and leaves the
	// manual key running.
	if err := e.RemoveSubscription(ctx, srv.URL); err != nil {
		t.Fatalf("RemoveSubscription: %v", err)
	}
	if got := e.CurrentLinks(); len(got) != 1 || got[0] != manual {
		t.Errorf("after removal links = %v, want just the manual key", got)
	}
	if hits != 1 {
		t.Errorf("panel was fetched %d times, want exactly 1", hits)
	}
}
