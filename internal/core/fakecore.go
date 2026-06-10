package core

import (
	"context"
	"sync"
)

// FakeCore is an in-memory Core for unit tests: it records lifecycle calls and
// lets tests inject Start/Close errors. Safe for concurrent use.
type FakeCore struct {
	Label    string
	Config   []byte
	StartErr error
	CloseErr error

	mu         sync.Mutex
	StartCalls int
	CloseCalls int
	running    bool
}

func (f *FakeCore) Start(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.StartCalls++
	if f.StartErr != nil {
		return f.StartErr
	}
	f.running = true
	return nil
}

func (f *FakeCore) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.CloseCalls++
	f.running = false
	return f.CloseErr
}

// Running reports whether the fake is currently started.
func (f *FakeCore) Running() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

// FakeFactory returns a Factory that produces FakeCores, recording each one it
// builds in Built (in creation order) so tests can inspect configs and call
// counts. NewErrOn, if set for a label, makes construction fail for that label.
type FakeFactory struct {
	mu       sync.Mutex
	Built    []*FakeCore
	NewErrOn map[string]error
}

func NewFakeFactory() *FakeFactory {
	return &FakeFactory{NewErrOn: map[string]error{}}
}

func (ff *FakeFactory) Factory() Factory {
	return func(ctx context.Context, label string, configJSON []byte) (Core, error) {
		ff.mu.Lock()
		defer ff.mu.Unlock()
		if err := ff.NewErrOn[label]; err != nil {
			return nil, err
		}
		c := &FakeCore{Label: label, Config: configJSON}
		ff.Built = append(ff.Built, c)
		return c, nil
	}
}

// BuiltFor returns the FakeCores created for a given label, in order.
func (ff *FakeFactory) BuiltFor(label string) []*FakeCore {
	ff.mu.Lock()
	defer ff.mu.Unlock()
	var out []*FakeCore
	for _, c := range ff.Built {
		if c.Label == label {
			out = append(out, c)
		}
	}
	return out
}
