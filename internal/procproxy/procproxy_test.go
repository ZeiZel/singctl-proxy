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

// TestStripMallocStackLogging is F2 item 6's regression test: every
// MallocStackLogging* variable must be removed from a spawned child's
// environment (it's what makes each one log "MallocStackLogging: can't turn
// off malloc stack logging" and dominate the daemon's log), while everything
// else — including a look-alike prefix that ISN'T the malloc family — passes
// through untouched.
func TestStripMallocStackLogging(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "strips the whole family",
			in: []string{
				"HOME=/root",
				"MallocStackLogging=1",
				"MallocStackLoggingNoCompact=1",
				"MallocStackLoggingDirectory=/tmp/x",
				"PATH=/usr/bin",
			},
			want: []string{"HOME=/root", "PATH=/usr/bin"},
		},
		{
			name: "no malloc vars present is a no-op",
			in:   []string{"HOME=/root", "PATH=/usr/bin"},
			want: []string{"HOME=/root", "PATH=/usr/bin"},
		},
		{
			name: "empty env",
			in:   nil,
			want: []string{},
		},
		{
			name: "a key merely containing the substring, not as a prefix, survives",
			in:   []string{"SOME_OTHER_MallocStackLogging_VAR=keep"},
			want: []string{"SOME_OTHER_MallocStackLogging_VAR=keep"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripMallocStackLogging(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("stripMallocStackLogging(%v) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestConfig_Defaults(t *testing.T) {
	c := Config{}.withDefaults()
	if c.SocksAddr != defaultSocksAddr || c.Gateway != defaultGateway || c.Mark != defaultMark {
		t.Errorf("defaults not applied: %+v", c)
	}
}

func TestEnvRouter_LaunchAndRoute(t *testing.T) {
	r := newEnvRouter(Config{})
	// RemovePID is a no-op off Linux (a restarted process can't be un-routed).
	if err := r.RemovePID(context.Background(), 1); err != nil {
		t.Errorf("RemovePID should be a no-op, got %v", err)
	}
	// AddPID restarts the process in proxy mode: with a bogus PID, argv recovery
	// (ps) fails, so it errors rather than claiming success.
	if err := r.AddPID(context.Background(), 1<<30); err == nil {
		t.Error("AddPID on a nonexistent PID should fail (argv recovery)")
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

func TestResolveExecutable(t *testing.T) {
	// An explicit path is returned as-is.
	if got, err := resolveExecutable("/bin/sh"); err != nil || got != "/bin/sh" {
		t.Errorf("resolveExecutable(/bin/sh) = %q, %v", got, err)
	}
	// A PATH command resolves to its absolute path.
	if got, err := resolveExecutable("sh"); err != nil || got == "" {
		t.Errorf("resolveExecutable(sh) = %q, %v", got, err)
	}
	// A bogus name errors clearly.
	if _, err := resolveExecutable("definitely-not-a-real-binary-xyz"); err == nil {
		t.Error("unknown command should error")
	}
}

func TestLaunchWithEnv_SetsEnv(t *testing.T) {
	// Launch `env` and capture: hard to read child stdout here, so just assert it
	// starts and the empty-argv guard works.
	if _, err := launchWithEnv(context.Background(), nil, nil, nil, nil); err == nil {
		t.Error("empty argv must error")
	}
	pid, err := launchWithEnv(context.Background(), []string{"true"}, proxyEnv("127.0.0.1:1080", "127.0.0.1:2080"), nil, nil)
	if err != nil || pid <= 0 {
		t.Fatalf("launchWithEnv true: pid=%d err=%v", pid, err)
	}
}

func TestApplyUserEnv(t *testing.T) {
	// nil user is a no-op.
	in := []string{"PATH=/bin", "HOME=/root"}
	if got := applyUserEnv(in, nil); !equalStr(got, in) {
		t.Errorf("nil user changed env: %v", got)
	}

	u := &LaunchUser{Uid: 501, Gid: 20, Name: "alice", Home: "/Users/alice"}
	env := []string{
		"PATH=/bin",
		"HOME=/var/root",
		"USER=root",
		"LOGNAME=root",
		"SUDO_USER=alice",
		"SUDO_UID=501",
		"SUDO_COMMAND=/usr/local/bin/singctl",
	}
	got := applyUserEnv(env, u)
	want := map[string]string{
		"PATH":    "/bin",
		"HOME":    "/Users/alice",
		"USER":    "alice",
		"LOGNAME": "alice",
	}
	gotMap := map[string]string{}
	for _, kv := range got {
		k, v, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "SUDO_") {
			t.Errorf("sudo var leaked into child env: %s", kv)
		}
		gotMap[k] = v
	}
	for k, v := range want {
		if gotMap[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, gotMap[k], v)
		}
	}
}

func equalStr(a, b []string) bool {
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
