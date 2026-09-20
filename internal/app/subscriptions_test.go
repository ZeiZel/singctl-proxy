package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"singctl/internal/sub"
)

// --- fake sub.Fetcher -------------------------------------------------------

// fakeFetchResult is one programmed response for a URL: either a Result or an
// error, never both consumed at once (Fetch returns exactly what was set).
type fakeFetchResult struct {
	result sub.Result
	err    error
}

// fakeSubFetcher is a programmable, in-memory sub.Fetcher: tests set a response
// per URL (swappable between calls, e.g. to simulate a refresh that starts
// failing) and can read back how many times each URL was fetched. It never
// touches the network.
type fakeSubFetcher struct {
	mu    sync.Mutex
	resp  map[string]fakeFetchResult
	calls map[string]int
}

func newFakeSubFetcher() *fakeSubFetcher {
	return &fakeSubFetcher{resp: map[string]fakeFetchResult{}, calls: map[string]int{}}
}

// set programs the response for url. Safe to call again later (e.g. between an
// initial AddSubscription and a subsequent RefreshSubscriptions) to change
// what the next fetch of that URL returns.
func (f *fakeSubFetcher) set(url string, result sub.Result, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resp[url] = fakeFetchResult{result: result, err: err}
}

func (f *fakeSubFetcher) Fetch(_ context.Context, url string) (sub.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[url]++
	r, ok := f.resp[url]
	if !ok {
		return sub.Result{}, fmt.Errorf("fakeSubFetcher: no response programmed for %s", url)
	}
	return r.result, r.err
}

func (f *fakeSubFetcher) callCount(url string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[url]
}

// --- fake subscription saver -------------------------------------------------

// recordingSubSaver captures every slice passed to SetSubscriptionDeps' save
// hook, so a test can assert on exactly what got "persisted" without touching
// disk.
type recordingSubSaver struct {
	mu    sync.Mutex
	calls [][]sub.Subscription
}

func (s *recordingSubSaver) save(subs []sub.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := make([]sub.Subscription, len(subs))
	copy(snap, subs)
	s.calls = append(s.calls, snap)
	return nil
}

func (s *recordingSubSaver) last() []sub.Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return nil
	}
	return s.calls[len(s.calls)-1]
}

// recordingManualSaver captures every string passed to SetSaver's hook (the
// profile.txt writer), so a test can assert only manual keys ever reach it.
type recordingManualSaver struct {
	mu    sync.Mutex
	calls []string
}

func (s *recordingManualSaver) save(v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, v)
	return nil
}

func (s *recordingManualSaver) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.calls))
	copy(out, s.calls)
	return out
}

// --- link fixtures -----------------------------------------------------------

// vlessLink builds a distinct, valid vless:// share link. Two calls with the
// same host/port/name produce byte-identical links (used to test dedup);
// different arguments always produce distinct ones.
func vlessLink(host string, port int, name string) string {
	return fmt.Sprintf("vless://4ce58870-27d3-489b-87a0-3109db4fb919@%s:%d?security=tls#%s", host, port, name)
}

// --- 1. merge + priority -----------------------------------------------------

// A subscription's servers must land AFTER every manual key, in the
// subscription's own configured order, and a manual key must win outright
// over a subscription that happens to repeat the very same server — losing
// either property would let a refresh silently reorder or evict a
// hand-picked failover priority.
func TestEffectiveLinks_ManualFirst_ThenSubscriptionOrder(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()

	m1 := vlessLink("m1.example", 443, "manual-1")
	m2 := vlessLink("m2.example", 443, "manual-2")
	if err := e.LoadLink(ctx, strings.Join([]string{m1, m2}, "\n")); err != nil {
		t.Fatalf("LoadLink: %v", err)
	}

	s1a := vlessLink("s1a.example", 443, "sub1-a")
	s1b := vlessLink("s1b.example", 443, "sub1-b")
	e.RestoreSubscriptions([]sub.Subscription{
		{URL: "https://panel.example/sub1", Links: []string{s1a, s1b}},
	})
	if err := e.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	want := []string{m1, m2, s1a, s1b}
	got := e.CurrentLinks()
	if !equalLinks(got, want) {
		t.Fatalf("CurrentLinks = %v, want %v (manual first, then subscription order)", got, want)
	}
}

// A manual key that happens to be byte-identical to a server a subscription
// also serves must keep its priority slot (the manual position) and must not
// additionally appear again at the subscription's position.
func TestEffectiveLinks_ManualWinsOverSameServerInSubscription(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()

	shared := vlessLink("shared.example", 443, "shared")
	other := vlessLink("other.example", 443, "other")
	if err := e.LoadLink(ctx, shared); err != nil {
		t.Fatalf("LoadLink: %v", err)
	}
	e.RestoreSubscriptions([]sub.Subscription{
		{URL: "https://panel.example/sub1", Links: []string{shared, other}},
	})
	if err := e.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	want := []string{shared, other}
	got := e.CurrentLinks()
	if !equalLinks(got, want) {
		t.Fatalf("CurrentLinks = %v, want %v (one 'shared' entry, at the manual slot)", got, want)
	}
	// And DeleteLink on it must treat it as manual, not subscription-owned.
	if owner, owned := e.subscriptionOwning(shared); owned {
		t.Fatalf("shared link reported owned by subscription %q; manual must win", owner)
	}
}

// --- 2. dedup across subscriptions -------------------------------------------

// The same server appearing in two different subscriptions must collapse to
// one effective entry — a duplicate would become a second urltest member for
// a server that is, in reality, one endpoint.
func TestEffectiveLinks_DedupAcrossSubscriptions(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()

	dup := vlessLink("dup.example", 443, "dup")
	uniqueA := vlessLink("a.example", 443, "a")
	uniqueB := vlessLink("b.example", 443, "b")
	e.RestoreSubscriptions([]sub.Subscription{
		{URL: "https://panel.example/sub1", Links: []string{uniqueA, dup}},
		{URL: "https://panel.example/sub2", Links: []string{dup, uniqueB}},
	})
	if err := e.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	got := e.CurrentLinks()
	want := []string{uniqueA, dup, uniqueB} // dup kept once, at its FIRST occurrence
	if !equalLinks(got, want) {
		t.Fatalf("CurrentLinks = %v, want %v (dup collapsed to one entry)", got, want)
	}
}

// --- 3. persistence split -----------------------------------------------------

// Only manual keys may ever reach the profile.txt saver; subscription servers
// must never be written there, or a restart would "promote" them to manual
// keys that a subsequent refresh could no longer safely rewrite.
func TestPersistence_OnlyManualKeysReachTheProfileSaver(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()
	manualSaver := &recordingManualSaver{}
	e.SetSaver(manualSaver.save)

	m1 := vlessLink("m1.example", 443, "manual-1")
	if err := e.LoadLink(ctx, m1); err != nil {
		t.Fatalf("LoadLink: %v", err)
	}

	s1 := vlessLink("s1.example", 443, "sub-1")
	e.RestoreSubscriptions([]sub.Subscription{
		{URL: "https://panel.example/sub1", Links: []string{s1}},
	})
	if err := e.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if len(manualSaver.calls) == 0 {
		t.Fatal("profile saver was never called")
	}
	for i, call := range manualSaver.all() {
		if strings.Contains(call, "s1.example") {
			t.Fatalf("save call %d = %q, contains a subscription server", i, call)
		}
		if strings.TrimSpace(call) != m1 {
			t.Fatalf("save call %d = %q, want exactly the manual key %q", i, call, m1)
		}
	}
}

// --- 4. ownership guards ------------------------------------------------------

// DeleteLink/RenameLink must refuse to touch a subscription-owned index (a
// refresh would silently undo the edit) while leaving both the manual set and
// the subscription's cached links completely unchanged.
func TestDeleteRenameLink_RefusesSubscriptionOwnedIndex(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()

	m1 := vlessLink("m1.example", 443, "manual-1")
	if err := e.LoadLink(ctx, m1); err != nil {
		t.Fatalf("LoadLink: %v", err)
	}
	s1 := vlessLink("s1.example", 443, "sub-1")
	s2 := vlessLink("s2.example", 443, "sub-2")
	e.RestoreSubscriptions([]sub.Subscription{
		{URL: "https://panel.example/sub1", Links: []string{s1, s2}},
	})
	if err := e.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	before := e.CurrentLinks() // [m1, s1, s2]
	if len(before) != 3 {
		t.Fatalf("precondition: CurrentLinks = %v, want 3 entries", before)
	}

	for _, idx := range []int{1, 2} { // s1, s2
		if err := e.DeleteLink(ctx, idx); err == nil {
			t.Errorf("DeleteLink(%d) on subscription-owned index: want error, got nil", idx)
		} else if !strings.Contains(err.Error(), "panel.example") {
			t.Errorf("DeleteLink(%d) error = %q, want it to name the subscription", idx, err)
		}
		if err := e.RenameLink(ctx, idx, "new-name"); err == nil {
			t.Errorf("RenameLink(%d) on subscription-owned index: want error, got nil", idx)
		} else if !strings.Contains(err.Error(), "panel.example") {
			t.Errorf("RenameLink(%d) error = %q, want it to name the subscription", idx, err)
		}
	}

	after := e.CurrentLinks()
	if !equalLinks(after, before) {
		t.Fatalf("CurrentLinks changed after refused edits: got %v, want unchanged %v", after, before)
	}
	subs := e.Subscriptions()
	if len(subs) != 1 || !equalLinks(subs[0].Links, []string{s1, s2}) {
		t.Fatalf("subscription cache mutated by a refused edit: %+v", subs)
	}
}

// A manual key must still be deletable/renameable by index even while
// subscription servers occupy the tail of the effective list, and the index
// translation from the effective (merged) list back to the manual slice must
// land on the right entry regardless of whether the manual key sits at the
// front or the back of the manual block that precedes the subscription's
// servers.
func TestDeleteRenameLink_ManualKeyStillWorks_WithSubscriptionServersPresent(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()

	m1 := vlessLink("m1.example", 443, "manual-1")
	m2 := vlessLink("m2.example", 443, "manual-2")
	if err := e.LoadLink(ctx, strings.Join([]string{m1, m2}, "\n")); err != nil {
		t.Fatalf("LoadLink: %v", err)
	}
	s1 := vlessLink("s1.example", 443, "sub-1")
	s2 := vlessLink("s2.example", 443, "sub-2")
	e.RestoreSubscriptions([]sub.Subscription{
		{URL: "https://panel.example/sub1", Links: []string{s1, s2}},
	})
	if err := e.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	// effective = [m1, m2, s1, s2]

	// Rename m2 — the manual key immediately adjacent to the subscription
	// block — and confirm only it changed, with s1/s2 untouched.
	if err := e.RenameLink(ctx, 1, "renamed-m2"); err != nil {
		t.Fatalf("RenameLink(1) on manual key: %v", err)
	}
	got := e.CurrentLinks()
	if len(got) != 4 {
		t.Fatalf("CurrentLinks after rename = %v, want 4 entries", got)
	}
	if got[0] != m1 {
		t.Errorf("CurrentLinks[0] = %q, want untouched m1 %q", got[0], m1)
	}
	if !strings.Contains(got[1], "renamed-m2") {
		t.Errorf("CurrentLinks[1] = %q, want it renamed to renamed-m2", got[1])
	}
	if got[2] != s1 || got[3] != s2 {
		t.Errorf("subscription servers moved/changed: got [%q %q], want [%q %q]", got[2], got[3], s1, s2)
	}

	// Delete m1 — the manual key at the very front — and confirm the
	// remaining manual key plus both subscription servers shift up correctly.
	if err := e.DeleteLink(ctx, 0); err != nil {
		t.Fatalf("DeleteLink(0) on manual key: %v", err)
	}
	got = e.CurrentLinks()
	want := []string{got[0], s1, s2} // got[0] is whatever m2 renamed to
	if len(got) != 3 || !strings.Contains(got[0], "renamed-m2") || got[1] != s1 || got[2] != s2 {
		t.Fatalf("CurrentLinks after deleting m1 = %v, want [renamed-m2, %q, %q]", got, s1, s2)
	}
	_ = want
}

// --- 5. AddSubscription -------------------------------------------------------

func TestAddSubscription_RejectsNonHTTPURL(t *testing.T) {
	e, _ := newExecutor()
	// A pasted vless:// share link is a plausible user error: it must be
	// rejected with a message pointing at KEYS-ADD, not sent to the fetcher.
	fetcher := newFakeSubFetcher()
	e.SetSubscriptionDeps(fetcher, nil)

	pastedKey := vlessLink("x.example", 443, "x")
	err := e.AddSubscription(context.Background(), pastedKey)
	if err == nil {
		t.Fatal("AddSubscription with a vless:// URL: want error, got nil")
	}
	if !strings.Contains(err.Error(), "http(s)") {
		t.Errorf("error = %q, want it to explain http(s) is required", err)
	}
	if len(e.Subscriptions()) != 0 {
		t.Error("a rejected URL must not be registered")
	}
	if got := fetcher.callCount(pastedKey); got != 0 {
		t.Errorf("fetcher was called %d times for a rejected URL, want 0", got)
	}
}

func TestAddSubscription_RejectsDuplicateURL(t *testing.T) {
	e, _ := newExecutor()
	fetcher := newFakeSubFetcher()
	url := "https://panel.example/sub1"
	link1 := vlessLink("a.example", 443, "a")
	fetcher.set(url, sub.Result{Links: []string{link1}}, nil)
	e.SetSubscriptionDeps(fetcher, nil)

	if err := e.AddSubscription(context.Background(), url); err != nil {
		t.Fatalf("first AddSubscription: %v", err)
	}
	if err := e.AddSubscription(context.Background(), url); err == nil {
		t.Fatal("second AddSubscription with the same URL: want error, got nil")
	}
	if len(e.Subscriptions()) != 1 {
		t.Errorf("Subscriptions() = %d, want exactly 1 after a rejected duplicate", len(e.Subscriptions()))
	}
}

func TestAddSubscription_FailsWithoutAFetcherWired(t *testing.T) {
	e, _ := newExecutor() // SetSubscriptionDeps never called
	err := e.AddSubscription(context.Background(), "https://panel.example/sub1")
	if err == nil {
		t.Fatal("AddSubscription without a fetcher: want error, got nil")
	}
	if len(e.Subscriptions()) != 0 {
		t.Error("no fetcher means nothing should be registered")
	}
}

func TestAddSubscription_Success_StoresPersistsAndApplies(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()
	fetcher := newFakeSubFetcher()
	saver := &recordingSubSaver{}
	e.SetSubscriptionDeps(fetcher, saver.save)

	url := "https://panel.example/sub1"
	s1 := vlessLink("a.example", 443, "a")
	s2 := vlessLink("b.example", 443, "b")
	fetcher.set(url, sub.Result{Links: []string{s1, s2}, Meta: sub.Meta{Title: "My Panel"}}, nil)

	if err := e.AddSubscription(ctx, url); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}

	subs := e.Subscriptions()
	if len(subs) != 1 || subs[0].URL != url || subs[0].Title != "My Panel" {
		t.Fatalf("Subscriptions() = %+v, want one record for %s titled 'My Panel'", subs, url)
	}
	if !equalLinks(subs[0].Links, []string{s1, s2}) {
		t.Errorf("stored Links = %v, want [%v %v]", subs[0].Links, s1, s2)
	}
	last := saver.last()
	if len(last) != 1 || last[0].URL != url {
		t.Fatalf("subscription saver last call = %+v, want one record for %s", last, url)
	}
	if got := e.CurrentLinks(); !equalLinks(got, []string{s1, s2}) {
		t.Errorf("CurrentLinks = %v, want the subscription's servers added to the effective set", got)
	}
}

// --- 6. refresh resilience ----------------------------------------------------

// A subscription whose refetch fails must keep its previously cached links
// and record the error, while a sibling subscription that succeeds must still
// update — a panel being briefly unreachable must never take a working proxy
// (or a working sibling subscription) down.
func TestRefreshSubscriptions_OneFailsOneSucceeds(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()
	fetcher := newFakeSubFetcher()
	e.SetSubscriptionDeps(fetcher, nil)

	urlOK := "https://panel.example/ok"
	urlBad := "https://panel.example/bad"
	okOld := vlessLink("ok-old.example", 443, "ok-old")
	badCached := vlessLink("bad-cached.example", 443, "bad-cached")
	fetcher.set(urlOK, sub.Result{Links: []string{okOld}}, nil)
	fetcher.set(urlBad, sub.Result{Links: []string{badCached}}, nil)
	if err := e.AddSubscription(ctx, urlOK); err != nil {
		t.Fatalf("AddSubscription(ok): %v", err)
	}
	if err := e.AddSubscription(ctx, urlBad); err != nil {
		t.Fatalf("AddSubscription(bad): %v", err)
	}

	// Reprogram: ok gets a new server list, bad starts failing.
	okNew := vlessLink("ok-new.example", 443, "ok-new")
	fetcher.set(urlOK, sub.Result{Links: []string{okNew}}, nil)
	fetcher.set(urlBad, sub.Result{}, fmt.Errorf("panel unreachable"))

	changed, err := e.RefreshSubscriptions(ctx)
	if err == nil {
		t.Fatal("RefreshSubscriptions: want non-nil error (one subscription failed)")
	}
	if !strings.Contains(err.Error(), "panel unreachable") {
		t.Errorf("returned error = %q, want it to surface the failing subscription's error", err)
	}
	if changed != 1 {
		t.Errorf("changed = %d, want 1 (only the healthy subscription updated)", changed)
	}

	byURL := map[string]sub.Subscription{}
	for _, s := range e.Subscriptions() {
		byURL[s.URL] = s
	}
	if got := byURL[urlOK].Links; !equalLinks(got, []string{okNew}) {
		t.Errorf("healthy subscription Links = %v, want [%v] (updated)", got, okNew)
	}
	bad := byURL[urlBad]
	if !equalLinks(bad.Links, []string{badCached}) {
		t.Errorf("failing subscription Links = %v, want [%v] (cache preserved)", bad.Links, badCached)
	}
	if !strings.Contains(bad.LastError, "panel unreachable") {
		t.Errorf("failing subscription LastError = %q, want it to record the fetch error", bad.LastError)
	}

	effective := e.CurrentLinks()
	if !equalLinks(effective, []string{okNew, badCached}) {
		t.Errorf("CurrentLinks = %v, want [%v %v] (cached server survives the failed refresh)", effective, okNew, badCached)
	}
}

// A refetch that returns links which fail to parse (e.g. an HTML login page
// slipped past the panel's auth check) must not wipe the cached, working
// server list — losing it would strand the user with zero servers over what
// looked like a routine background refresh.
func TestRefreshSubscriptions_GarbageResponseDoesNotWipeCache(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()
	fetcher := newFakeSubFetcher()
	e.SetSubscriptionDeps(fetcher, nil)

	url := "https://panel.example/sub1"
	good := vlessLink("good.example", 443, "good")
	fetcher.set(url, sub.Result{Links: []string{good}}, nil)
	if err := e.AddSubscription(ctx, url); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}

	// Simulate an HTML page slipping through where links were expected.
	fetcher.set(url, sub.Result{Links: []string{"<html>please log in</html>"}}, nil)

	changed, err := e.RefreshSubscriptions(ctx)
	if err == nil {
		t.Fatal("RefreshSubscriptions with unparseable links: want a non-nil error")
	}
	if changed != 0 {
		t.Errorf("changed = %d, want 0 (garbage must not count as an update)", changed)
	}

	subs := e.Subscriptions()
	if len(subs) != 1 || !equalLinks(subs[0].Links, []string{good}) {
		t.Fatalf("subscription cache after garbage refresh = %+v, want the original [%v] preserved", subs, good)
	}
	if subs[0].LastError == "" {
		t.Error("LastError should record the unusable-links failure")
	}
	if got := e.CurrentLinks(); !equalLinks(got, []string{good}) {
		t.Errorf("CurrentLinks = %v, want [%v] (cached server still effective)", got, good)
	}
}

// --- 8. RemoveSubscription ----------------------------------------------------

// Removing a subscription must drop exactly its own servers, leaving manual
// keys and every other subscription's servers untouched.
func TestRemoveSubscription_DropsOnlyItsOwnServers(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()
	fetcher := newFakeSubFetcher()
	saver := &recordingSubSaver{}
	e.SetSubscriptionDeps(fetcher, saver.save)

	m1 := vlessLink("m1.example", 443, "manual-1")
	if err := e.LoadLink(ctx, m1); err != nil {
		t.Fatalf("LoadLink: %v", err)
	}

	url1, url2 := "https://panel.example/sub1", "https://panel.example/sub2"
	s1 := vlessLink("s1.example", 443, "s1")
	s2 := vlessLink("s2.example", 443, "s2")
	fetcher.set(url1, sub.Result{Links: []string{s1}}, nil)
	fetcher.set(url2, sub.Result{Links: []string{s2}}, nil)
	if err := e.AddSubscription(ctx, url1); err != nil {
		t.Fatalf("AddSubscription(1): %v", err)
	}
	if err := e.AddSubscription(ctx, url2); err != nil {
		t.Fatalf("AddSubscription(2): %v", err)
	}

	if err := e.RemoveSubscription(ctx, url1); err != nil {
		t.Fatalf("RemoveSubscription: %v", err)
	}

	subs := e.Subscriptions()
	if len(subs) != 1 || subs[0].URL != url2 {
		t.Fatalf("Subscriptions() after remove = %+v, want only %s left", subs, url2)
	}
	got := e.CurrentLinks()
	want := []string{m1, s2}
	if !equalLinks(got, want) {
		t.Fatalf("CurrentLinks after RemoveSubscription = %v, want %v", got, want)
	}
	last := saver.last()
	if len(last) != 1 || last[0].URL != url2 {
		t.Errorf("subscription saver last call = %+v, want only %s persisted", last, url2)
	}
}

func TestRemoveSubscription_UnknownURL(t *testing.T) {
	e, _ := newExecutor()
	if err := e.RemoveSubscription(context.Background(), "https://panel.example/nope"); err == nil {
		t.Fatal("RemoveSubscription for an unregistered URL: want error, got nil")
	}
}

// --- 9. due-time logic --------------------------------------------------------

// anySubscriptionDue drives the background refresher: a subscription that has
// never been fetched must be due immediately (so a fresh install picks up
// servers without waiting out a full interval), and one fetched recently
// within its own interval must not be due yet.
func TestAnySubscriptionDue(t *testing.T) {
	e, _ := newExecutor()

	e.RestoreSubscriptions([]sub.Subscription{
		{URL: "https://panel.example/sub1", Meta: sub.Meta{UpdateInterval: 24 * time.Hour}},
	})
	if !e.anySubscriptionDue() {
		t.Error("a never-fetched subscription must be due immediately")
	}

	e.RestoreSubscriptions([]sub.Subscription{
		{URL: "https://panel.example/sub1", LastUpdate: time.Now(), Meta: sub.Meta{UpdateInterval: 24 * time.Hour}},
	})
	if e.anySubscriptionDue() {
		t.Error("a subscription fetched moments ago, well within its interval, must not be due")
	}

	e.RestoreSubscriptions([]sub.Subscription{
		{URL: "https://panel.example/sub1", LastUpdate: time.Now().Add(-2 * time.Hour), Meta: sub.Meta{UpdateInterval: time.Hour}},
	})
	if !e.anySubscriptionDue() {
		t.Error("a subscription whose interval has elapsed since its last fetch must be due again")
	}
}

// --- helpers -------------------------------------------------------------

func equalLinks(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if strings.TrimSpace(got[i]) != strings.TrimSpace(want[i]) {
			return false
		}
	}
	return true
}
