package main

import (
	"context"
	"testing"
	"time"

	"singctl/internal/app"
	"singctl/internal/core"
	"singctl/internal/protocol/all"
)

type stubProber struct{}

func (stubProber) PhysicalDefault() (string, error) { return "en0", nil }

type stubRoutes struct{}

func (stubRoutes) CleanupOrphans() error { return nil }

// A fresh install has no key and no subscription. The LaunchDaemon (KeepAlive)
// runs the daemon anyway, and the GUI can only add the first key over the
// control socket — so runHeadless must keep running instead of failing. 1.13.1
// exited with "headless mode needs a key or a subscription", which left launchd
// restarting it forever and the app stuck on "daemon offline".
func TestRunHeadless_NoKeyKeepsRunning(t *testing.T) {
	notes := make(chan any, 16)
	executor := app.NewExecutor(core.NewFakeFactory().Factory(), all.Registry(), stubProber{}, stubRoutes{}, notes)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runHeadless(ctx, executor, notes, "", &cli{}) }()

	select {
	case err := <-done:
		t.Fatalf("runHeadless returned without a key before shutdown: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runHeadless did not stop after cancellation")
	}
}
