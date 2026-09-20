package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"singctl/internal/sub"
)

// This file owns the subscription half of the key set. The rule it enforces
// everywhere: a subscription's servers belong to the subscription. They are
// never persisted as manual keys, never renamed and never deleted individually
// — a refresh would silently undo any of that, which is worse than refusing.

// SetSubscriptionDeps wires the network fetcher and the persistence hook. Both
// are optional: a client that must not reach the network (or must not write to
// disk) simply leaves them nil, and subscription mutation then fails loudly
// instead of half-working.
func (e *Executor) SetSubscriptionDeps(f sub.Fetcher, save func([]sub.Subscription) error) {
	e.subMu.Lock()
	defer e.subMu.Unlock()
	e.fetcher = f
	e.saveSubs = save
}

// RestoreSubscriptions seeds the registry from disk at startup. It does NOT
// fetch: the daemon must come up with the servers it had last time even when
// the panel is unreachable, and the refresher picks up the stale ones shortly
// after.
func (e *Executor) RestoreSubscriptions(subs []sub.Subscription) {
	e.subMu.Lock()
	defer e.subMu.Unlock()
	e.subs = append(e.subs[:0], subs...)
}

// Subscriptions returns a snapshot of the configured subscriptions.
func (e *Executor) Subscriptions() []sub.Subscription {
	e.subMu.Lock()
	defer e.subMu.Unlock()
	out := make([]sub.Subscription, len(e.subs))
	copy(out, e.subs)
	return out
}

// manualLinks returns the keys the user entered by hand.
func (e *Executor) manualLinks() []string {
	e.subMu.Lock()
	defer e.subMu.Unlock()
	out := make([]string, len(e.manual))
	copy(out, e.manual)
	return out
}

// effectiveLinks is the set the proxy actually runs: manual keys first (so a
// hand-added server keeps priority in the failover group), then each
// subscription's servers in configured order. Duplicates are dropped, keeping
// the first occurrence — the same server appearing in two subscriptions must
// not become two urltest members.
func (e *Executor) effectiveLinks() []string {
	e.subMu.Lock()
	defer e.subMu.Unlock()

	seen := make(map[string]bool, len(e.manual))
	out := make([]string, 0, len(e.manual))
	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] {
			return
		}
		seen[raw] = true
		out = append(out, raw)
	}
	for _, l := range e.manual {
		add(l)
	}
	for _, s := range e.subs {
		for _, l := range s.Links {
			add(l)
		}
	}
	return out
}

// subscriptionOwning reports which subscription supplies a link, if any. Used
// to refuse edits that a refresh would undo.
func (e *Executor) subscriptionOwning(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	e.subMu.Lock()
	defer e.subMu.Unlock()
	for _, l := range e.manual {
		if strings.TrimSpace(l) == raw {
			return "", false // a manual key wins even if a subscription repeats it
		}
	}
	for _, s := range e.subs {
		for _, l := range s.Links {
			if strings.TrimSpace(l) == raw {
				return s.Label(), true
			}
		}
	}
	return "", false
}

// AddSubscription registers a subscription URL, fetches it immediately and
// reloads. The fetch is synchronous on purpose: a URL that does not resolve to
// a usable server list must fail in front of the user, not silently register
// and stay empty.
func (e *Executor) AddSubscription(ctx context.Context, raw string) error {
	raw = strings.TrimSpace(raw)
	if err := validateSubURL(raw); err != nil {
		return err
	}
	e.subMu.Lock()
	for _, s := range e.subs {
		if s.URL == raw {
			e.subMu.Unlock()
			return fmt.Errorf("subscription already added: %s", raw)
		}
	}
	fetcher := e.fetcher
	e.subMu.Unlock()
	if fetcher == nil {
		return fmt.Errorf("subscriptions are not available in this mode")
	}

	record := sub.Subscription{URL: raw, AddedAt: time.Now()}
	if err := e.fetchInto(ctx, fetcher, &record); err != nil {
		return err
	}

	e.subMu.Lock()
	e.subs = append(e.subs, record)
	e.subMu.Unlock()

	e.persistSubscriptions()
	return e.applyPreservingMode(ctx)
}

// RemoveSubscription drops a subscription and every server it contributed.
func (e *Executor) RemoveSubscription(ctx context.Context, raw string) error {
	raw = strings.TrimSpace(raw)
	e.subMu.Lock()
	kept := make([]sub.Subscription, 0, len(e.subs))
	found := false
	for _, s := range e.subs {
		if s.URL == raw {
			found = true
			continue
		}
		kept = append(kept, s)
	}
	e.subs = kept
	e.subMu.Unlock()
	if !found {
		return fmt.Errorf("no such subscription: %s", raw)
	}
	e.persistSubscriptions()
	return e.applyPreservingMode(ctx)
}

// RefreshSubscriptions refetches every configured subscription and reloads if
// anything changed. It returns the number that were updated.
//
// A subscription that fails to fetch keeps its previously cached servers and
// records the error: a panel being briefly unreachable must never take a
// working proxy down.
func (e *Executor) RefreshSubscriptions(ctx context.Context) (int, error) {
	e.subMu.Lock()
	fetcher := e.fetcher
	current := make([]sub.Subscription, len(e.subs))
	copy(current, e.subs)
	e.subMu.Unlock()

	if len(current) == 0 {
		return 0, nil
	}
	if fetcher == nil {
		return 0, fmt.Errorf("subscriptions are not available in this mode")
	}

	changed := 0
	var firstErr error
	for i := range current {
		before := strings.Join(current[i].Links, "\n")
		if err := e.fetchInto(ctx, fetcher, &current[i]); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if strings.Join(current[i].Links, "\n") != before {
			changed++
		}
	}

	e.subMu.Lock()
	// Re-match by URL rather than by index: the user may have added or removed
	// a subscription while the fetches were in flight.
	for _, updated := range current {
		for i := range e.subs {
			if e.subs[i].URL == updated.URL {
				e.subs[i] = updated
				break
			}
		}
	}
	e.subMu.Unlock()

	e.persistSubscriptions()
	if changed > 0 {
		if err := e.applyPreservingMode(ctx); err != nil {
			return changed, err
		}
	}
	return changed, firstErr
}

// fetchInto performs one fetch and folds the outcome into the record, leaving
// the cached links untouched on failure.
func (e *Executor) fetchInto(ctx context.Context, fetcher sub.Fetcher, record *sub.Subscription) error {
	result, err := fetcher.Fetch(ctx, record.URL)
	if err != nil {
		record.LastError = err.Error()
		return err
	}
	// Validate before adopting: a panel that starts serving garbage (or an
	// HTML login page) must not wipe out a working server list.
	if _, perr := e.registry.ParseAll(result.Links); perr != nil {
		record.LastError = perr.Error()
		return fmt.Errorf("subscription %s returned unusable links: %w", record.URL, perr)
	}
	record.Links = result.Links
	record.Meta = result.Meta
	if result.Meta.Title != "" {
		record.Title = result.Meta.Title
	}
	record.LastUpdate = time.Now()
	record.LastError = ""
	return nil
}

// StartSubscriptionRefresher keeps subscriptions current in the background
// until ctx is cancelled. It ticks often and refetches only what is actually
// due, so a panel asking for hourly updates and one asking for daily ones can
// coexist without either being polled more than it wants.
func (e *Executor) StartSubscriptionRefresher(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !e.anySubscriptionDue() {
					continue
				}
				fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				_, _ = e.RefreshSubscriptions(fetchCtx)
				cancel()
			}
		}
	}()
}

func (e *Executor) anySubscriptionDue() bool {
	now := time.Now()
	e.subMu.Lock()
	defer e.subMu.Unlock()
	for _, s := range e.subs {
		if due := s.DueAt(); due.IsZero() || now.After(due) {
			return true
		}
	}
	return false
}

func (e *Executor) persistSubscriptions() {
	e.subMu.Lock()
	save := e.saveSubs
	snapshot := make([]sub.Subscription, len(e.subs))
	copy(snapshot, e.subs)
	e.subMu.Unlock()
	if save != nil {
		_ = save(snapshot)
	}
}

// Reload rebuilds the runtime from the current manual keys and subscription
// servers. Callers use it when the inputs changed underneath them — notably at
// startup, where a daemon may have no manual keys at all and run entirely off
// its subscriptions.
func (e *Executor) Reload(ctx context.Context) error { return e.applyEffective(ctx) }

// clearLinks tears the runtime down when nothing is left to run: no manual
// keys and no subscription servers. Shared by the "removed the last key" path
// and by applyEffective, so both leave exactly the same state behind.
func (e *Executor) clearLinks(ctx context.Context) error {
	if err := e.Stop(ctx); err != nil {
		return err
	}
	if old := e.manager(); old != nil {
		_ = old.Shutdown(ctx)
	}
	e.mu.Lock()
	e.mgr = nil
	e.links = nil
	e.mu.Unlock()

	e.cfgMu.Lock()
	save := e.save
	e.cfgMu.Unlock()
	if save != nil {
		_ = save(strings.Join(e.manualLinks(), "\n"))
	}
	return nil
}

// applyPreservingMode rebuilds the runtime from the new effective set and
// re-enables whatever mode was running, so subscription changes take effect
// live exactly like manual key edits do.
func (e *Executor) applyPreservingMode(ctx context.Context) error {
	prev := e.StateLabel()
	if err := e.applyEffective(ctx); err != nil {
		return err
	}
	switch prev {
	case "vpn":
		return e.EnableVPN(ctx)
	case "proxy", "suspended":
		return e.EnableProxy(ctx)
	}
	return nil
}

// validateSubURL rejects anything that is not an http(s) URL up front, so a
// pasted share link or a typo produces a clear message instead of a confusing
// fetch failure later.
func validateSubURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("empty subscription URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid subscription URL %q: %w", raw, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return fmt.Errorf("subscription URL must be http(s), got %q — a share link (vless://…) is a key, add it with KEYS-ADD instead", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("subscription URL %q has no host", raw)
	}
	return nil
}
