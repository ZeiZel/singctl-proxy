package core

import (
	"context"
	"errors"
	"testing"
)

func TestFakeCore_Lifecycle(t *testing.T) {
	c := &FakeCore{Label: "proxy"}
	if c.Running() {
		t.Fatal("new core should not be running")
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !c.Running() || c.StartCalls != 1 {
		t.Fatalf("after Start: running=%v starts=%d", c.Running(), c.StartCalls)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if c.Running() || c.CloseCalls != 1 {
		t.Fatalf("after Close: running=%v closes=%d", c.Running(), c.CloseCalls)
	}
}

func TestFakeCore_StartErr(t *testing.T) {
	boom := errors.New("boom")
	c := &FakeCore{StartErr: boom}
	if err := c.Start(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if c.Running() {
		t.Fatal("failed Start must leave core not running")
	}
}

func TestFakeFactory_RecordsAndErrors(t *testing.T) {
	ff := NewFakeFactory()
	f := ff.Factory()

	c1, err := f(context.Background(), "proxy", []byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("build proxy: %v", err)
	}
	if fc := c1.(*FakeCore); string(fc.Config) != `{"a":1}` {
		t.Errorf("config not recorded: %s", fc.Config)
	}
	if got := ff.BuiltFor("proxy"); len(got) != 1 {
		t.Errorf("BuiltFor(proxy) = %d, want 1", len(got))
	}

	ff.NewErrOn["forwarder"] = errors.New("nope")
	if _, err := f(context.Background(), "forwarder", nil); err == nil {
		t.Error("expected forwarder construction to fail")
	}
}
