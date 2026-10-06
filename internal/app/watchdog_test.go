package app

import (
	"testing"
	"time"
)

func TestWatchdogRequiresSustainedGrowth(t *testing.T) {
	w := NewWatchdog(nil, WatchdogConfig{Interval: time.Minute, SustainedFor: 15 * time.Minute, GoroutineLimit: 1000, FileDescLimit: 1000, TrackerLimit: 1000})
	if w.leaking([]HealthSample{{Goroutines: 5, FileDescs: 10, TrackerCount: 5}}) {
		t.Fatal("one sample must not trigger watchdog")
	}
	samples := make([]HealthSample, 16)
	for i := range samples {
		samples[i] = HealthSample{Goroutines: 5 + i, FileDescs: 10 + i, TrackerCount: 5}
	}
	if w.leaking(samples) {
		t.Fatal("small growth before threshold must not trigger watchdog")
	}
	for i := range samples {
		samples[i] = HealthSample{Goroutines: 5 + i*7, FileDescs: 10 + i*3, TrackerCount: 5 + i*7}
	}
	if w.leaking(samples) {
		t.Fatal("ordinary workload growth must not trigger watchdog")
	}
	for i := range samples {
		samples[i] = HealthSample{Goroutines: 999, FileDescs: 999, TrackerCount: 999}
	}
	if w.leaking(samples) {
		t.Fatal("fixture below configured pressure must not trigger watchdog")
	}
	for i := range samples {
		samples[i] = HealthSample{Goroutines: 1000, FileDescs: 1000, TrackerCount: 1000}
	}
	if !w.leaking(samples) {
		t.Fatal("sustained pressure should trigger watchdog")
	}
}

func TestWatchdogThresholdsAreConservative(t *testing.T) {
	w := NewWatchdog(nil, WatchdogConfig{Interval: time.Minute, SustainedFor: 15 * time.Minute, GoroutineLimit: 10, FileDescLimit: 20, TrackerLimit: 30})
	samples := make([]HealthSample, 16)
	for i := range samples {
		samples[i] = HealthSample{Goroutines: 5, FileDescs: 10, TrackerCount: 5}
	}
	samples[len(samples)-1].Goroutines = 10
	if w.leaking(samples) {
		t.Fatal("late threshold spike must not trigger")
	}
	if w.leaking([]HealthSample{
		{Goroutines: 5, FileDescs: 10, TrackerCount: 5},
		{Goroutines: 6, FileDescs: 10, TrackerCount: 5},
	}) {
		t.Fatal("single-counter increase below threshold must not trigger")
	}
}

func TestWatchdogActualElapsedPressure(t *testing.T) {
	cfg := WatchdogConfig{Interval: time.Minute, SustainedFor: 15 * time.Minute, GoroutineLimit: 100}
	var window pressureWindow
	now := time.Unix(1, 0)
	high := HealthSample{Goroutines: 100}
	// Sixteen jittered observations spanning less than fifteen minutes cannot trigger.
	for i := 0; i < 16; i++ {
		if window.observe(now.Add(time.Duration(i)*59*time.Second), high, cfg) {
			t.Fatal("sample count replaced elapsed duration")
		}
	}
	if !window.observe(now.Add(15*time.Minute), high, cfg) {
		t.Fatal("full elapsed pressure did not trigger")
	}
}
func TestWatchdogAlternatingResourcePressure(t *testing.T) {
	cfg := WatchdogConfig{Interval: time.Minute, SustainedFor: 15 * time.Minute, GoroutineLimit: 100, FileDescLimit: 100}
	var window pressureWindow
	now := time.Unix(1, 0)
	for i := 0; i < 32; i++ {
		s := HealthSample{Goroutines: 100}
		if i%2 == 1 {
			s = HealthSample{FileDescs: 100}
		}
		if window.observe(now.Add(time.Duration(i)*time.Minute), s, cfg) {
			t.Fatal("alternating resources combined into a leak")
		}
	}
}
func TestWatchdogSamplingGapResetsPressure(t *testing.T) {
	cfg := WatchdogConfig{Interval: time.Minute, SustainedFor: 15 * time.Minute, GoroutineLimit: 100}
	var window pressureWindow
	now := time.Unix(1, 0)
	high := HealthSample{Goroutines: 100}
	window.observe(now, high, cfg)
	if window.observe(now.Add(time.Hour), high, cfg) {
		t.Fatal("sleep/sampling gap counted as sustained pressure")
	}
}
