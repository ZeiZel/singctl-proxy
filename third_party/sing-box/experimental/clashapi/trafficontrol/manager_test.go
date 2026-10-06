package trafficontrol

import (
	"sync"
	"testing"

	"github.com/gofrs/uuid/v5"
)

type testTracker struct{ metadata TrackerMetadata }

func (t *testTracker) Metadata() *TrackerMetadata { return &t.metadata }
func (t *testTracker) Close() error               { return nil }

func TestActiveConnectionsCountJoinLeave(t *testing.T) {
	m := NewManager()
	tt := &testTracker{metadata: TrackerMetadata{ID: uuid.Must(uuid.NewV4())}}
	m.Join(tt)
	if got := m.ActiveConnectionsCount(); got != 1 {
		t.Fatalf("active=%d, want 1", got)
	}
	m.Join(tt)
	if got := m.ActiveConnectionsCount(); got != 1 {
		t.Fatalf("duplicate join active=%d, want 1", got)
	}
	m.Leave(tt)
	if got := m.ActiveConnectionsCount(); got != 0 {
		t.Fatalf("active=%d, want 0", got)
	}
}

func TestActiveConnectionsCountConcurrent(t *testing.T) {
	m := NewManager()
	trackers := make([]*testTracker, 100)
	var wg sync.WaitGroup
	for i := range trackers {
		trackers[i] = &testTracker{metadata: TrackerMetadata{ID: uuid.Must(uuid.NewV4())}}
		wg.Add(1)
		go func(tt *testTracker) { defer wg.Done(); m.Join(tt); m.Leave(tt) }(trackers[i])
	}
	wg.Wait()
	if got := m.ActiveConnectionsCount(); got != 0 {
		t.Fatalf("active=%d, want 0", got)
	}
}
