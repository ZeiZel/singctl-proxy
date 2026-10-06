package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// HealthSample contains only process-local resource counters. TrackerCount is
// supplied by the embedded core when it can expose its own connection tracker.
type HealthSample struct {
	Goroutines   int
	FileDescs    int
	TrackerCount int
}

type HealthSampler func(context.Context) (HealthSample, error)

type WatchdogConfig struct {
	Interval       time.Duration
	SustainedFor   time.Duration
	GoroutineLimit int
	FileDescLimit  int
	TrackerLimit   int
	Logger         func(string)
	Exit           func()
	CooldownFile   string
	Cooldown       time.Duration
}

// Watchdog samples bounded self-process counters and exits once a sustained
// leak threshold is reached. It exits at most once per instance, preventing a
// rapid restart loop from hiding a persistent upstream leak.
type Watchdog struct {
	sample HealthSampler
	cfg    WatchdogConfig
}

func NewWatchdog(sample HealthSampler, cfg WatchdogConfig) *Watchdog {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	if cfg.SustainedFor <= 0 {
		cfg.SustainedFor = 15 * time.Minute
	}
	if cfg.Logger == nil {
		cfg.Logger = func(message string) { log.Print(message) }
	}
	if cfg.Exit == nil {
		cfg.Exit = func() { os.Exit(1) }
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = time.Hour
	}
	return &Watchdog{sample: sample, cfg: cfg}
}

func (w *Watchdog) Run(ctx context.Context) {
	if w.sample == nil {
		return
	}
	tick := time.NewTicker(w.cfg.Interval)
	defer tick.Stop()
	var pressure pressureWindow
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s, err := w.sample(ctx)
			if err != nil {
				w.cfg.Logger(fmt.Sprintf("watchdog sample failed: %v", err))
				pressure = pressureWindow{}
				continue
			}
			if pressure.observe(time.Now(), s, w.cfg) {
				if w.inCooldown() {
					w.cfg.Logger("watchdog threshold suppressed by cooldown")
					pressure = pressureWindow{}
					continue
				}
				w.cfg.Logger(fmt.Sprintf("watchdog sustained resource growth: %+v", s))
				w.markCooldown()
				w.cfg.Exit()
				return
			}
		}
	}
}

func (w *Watchdog) inCooldown() bool {
	if w.cfg.CooldownFile == "" {
		return false
	}
	info, err := os.Lstat(w.cfg.CooldownFile)
	return err == nil && info.Mode().IsRegular() && time.Since(info.ModTime()) < w.cfg.Cooldown
}

func (w *Watchdog) markCooldown() {
	if w.cfg.CooldownFile == "" {
		return
	}
	dir := filepath.Dir(w.cfg.CooldownFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	tmp, err := os.CreateTemp(dir, ".watchdog-cooldown-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	_ = tmp.Chmod(0o600)
	_, _ = tmp.WriteString(time.Now().UTC().Format(time.RFC3339Nano))
	_ = tmp.Close()
	_ = os.Rename(name, w.cfg.CooldownFile)
}

// pressureWindow tracks each resource independently using elapsed monotonic
// time. Alternating resource spikes never combine into one sustained leak.
type pressureWindow struct {
	since      [3]time.Time
	lastSample time.Time
}

func (p *pressureWindow) observe(now time.Time, s HealthSample, cfg WatchdogConfig) bool {
	if !p.lastSample.IsZero() && (now.Before(p.lastSample) || now.Sub(p.lastSample) > 2*cfg.Interval) {
		*p = pressureWindow{}
	}
	p.lastSample = now
	values := [3]int{s.Goroutines, s.FileDescs, s.TrackerCount}
	limits := [3]int{cfg.GoroutineLimit, cfg.FileDescLimit, cfg.TrackerLimit}
	leaking := false
	for i, value := range values {
		if limits[i] <= 0 || value < limits[i] {
			p.since[i] = time.Time{}
			continue
		}
		if p.since[i].IsZero() {
			p.since[i] = now
		}
		if now.Sub(p.since[i]) >= cfg.SustainedFor {
			leaking = true
		}
	}
	return leaking
}

// leaking evaluates regularly spaced fixture samples; production uses actual
// sample timestamps through pressureWindow.observe.
func (w *Watchdog) leaking(samples []HealthSample) bool {
	var pressure pressureWindow
	base := time.Unix(1, 0)
	for i, s := range samples {
		if pressure.observe(base.Add(time.Duration(i)*w.cfg.Interval), s, w.cfg) {
			return true
		}
	}
	return false
}

// SelfHealthSample reads only counters owned by this process. The optional
// tracker callback is kept outside this helper so tests and composition roots
// can supply a local sing-box tracker without inspecting another process.
func SelfHealthSample(ctx context.Context, tracker func() int) (HealthSample, error) {
	if err := ctx.Err(); err != nil {
		return HealthSample{}, err
	}
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return HealthSample{}, err
	}
	count := len(entries)
	return HealthSample{Goroutines: runtime.NumGoroutine(), FileDescs: count, TrackerCount: trackerCount(tracker)}, nil
}

func trackerCount(tracker func() int) int {
	if tracker == nil {
		return 0
	}
	return tracker()
}
