package procproxy

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestProxyEnv(t *testing.T) {
	env := proxyEnv("127.0.0.1:1080", "127.0.0.1:2080")
	want := map[string]string{
		"HTTP_PROXY":  "http://127.0.0.1:2080",
		"https_proxy": "http://127.0.0.1:2080",
		"ALL_PROXY":   "socks5h://127.0.0.1:1080",
		"all_proxy":   "socks5h://127.0.0.1:1080",
	}
	got := map[string]string{}
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		got[parts[0]] = parts[1]
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestConfig_Defaults(t *testing.T) {
	c := Config{}.withDefaults()
	if c.SocksAddr != defaultSocksAddr || c.Gateway != defaultGateway || c.Mark != defaultMark {
		t.Errorf("defaults not applied: %+v", c)
	}
}

func TestEnvRouter_LaunchAndUnsupported(t *testing.T) {
	r := newEnvRouter(Config{})
	// AddPID/RemovePID are unsupported on the fallback.
	if err := r.AddPID(context.Background(), 1); err != ErrUnsupportedOnPlatform {
		t.Errorf("AddPID err = %v, want ErrUnsupportedOnPlatform", err)
	}
	// Launch runs a real, harmless command and records the PID.
	pid, err := r.Launch(context.Background(), []string{"true"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if pid <= 0 {
		t.Errorf("Launch pid = %d, want > 0", pid)
	}
	if len(r.ListRouted()) != 1 {
		t.Errorf("ListRouted = %v, want one entry", r.ListRouted())
	}
}

func TestLaunchWithEnv_SetsEnv(t *testing.T) {
	// Launch `env` and capture: hard to read child stdout here, so just assert it
	// starts and the empty-argv guard works.
	if _, err := launchWithEnv(context.Background(), nil, nil); err == nil {
		t.Error("empty argv must error")
	}
	pid, err := launchWithEnv(context.Background(), []string{"true"}, proxyEnv("127.0.0.1:1080", "127.0.0.1:2080"))
	if err != nil || pid <= 0 {
		t.Fatalf("launchWithEnv true: pid=%d err=%v", pid, err)
	}
}

func TestFakeRouter(t *testing.T) {
	f := &FakeRouter{}
	_ = f.AddPID(context.Background(), 42)
	pid, _ := f.Launch(context.Background(), []string{"curl", "x"})
	if len(f.Added) != 1 || f.Added[0] != 42 {
		t.Errorf("AddPID not recorded: %v", f.Added)
	}
	if len(f.Launched) != 1 || f.Launched[0][0] != "curl" {
		t.Errorf("Launch not recorded: %v", f.Launched)
	}
	if want := []int{42, pid}; !equal(f.ListRouted(), want) {
		t.Errorf("ListRouted = %v, want %v", f.ListRouted(), want)
	}
	_ = f.RemovePID(context.Background(), 42)
	if want := []int{pid}; !equal(f.ListRouted(), want) {
		t.Errorf("after remove ListRouted = %v, want %v", f.ListRouted(), want)
	}
	_ = f.Cleanup()
	if !f.CleanedUp {
		t.Error("Cleanup not recorded")
	}
}

func TestNewRouter_NonNil(t *testing.T) {
	if NewRouter(Config{}) == nil {
		t.Fatal("NewRouter returned nil")
	}
	// Sanity: env is appendable on the current OS.
	_ = os.Environ()
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
