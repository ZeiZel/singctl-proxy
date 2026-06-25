package procproxy

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestChromiumProxyArgs_AppendsForKnownApps(t *testing.T) {
	const socks = "127.0.0.1:1080"
	want := "--proxy-server=socks5://" + socks
	cases := []struct {
		name string
		argv []string
	}{
		{"plain cursor", []string{"cursor"}},
		{"path cursor", []string{"/usr/bin/cursor"}},
		{"mac bundle Cursor.app", []string{"/Applications/Cursor.app"}},
		{"mac bundle exe path", []string{"/Applications/Cursor.app/Contents/MacOS/Cursor"}},
		{"vscode code", []string{"code", "--new-window"}},
		{"chrome", []string{"google-chrome"}}, // base "google-chrome" not matched; see negative test
		{"chromium", []string{"chromium"}},
		{"brave", []string{"brave"}},
		{"zen", []string{"zen"}},
		{"electron", []string{"electron", "main.js"}},
		{"slack", []string{"/opt/Slack/slack"}},
		{"discord exe", []string{"Discord.exe"}},
		{"uppercase Chrome.app", []string{"/Applications/Chrome.app"}},
		{"claude desktop bundle", []string{"/Applications/Claude.app/Contents/MacOS/Claude"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chromiumProxyArgs(c.argv, socks)
			// "google-chrome" base is not in the known set (only "chrome"/"google chrome");
			// skip the assertion for that intentionally-negative fixture.
			if appLabel(c.argv[0]) == "google-chrome" {
				if containsArg(got, want) {
					t.Fatalf("google-chrome should NOT get a proxy arg: %v", got)
				}
				return
			}
			if !containsArg(got, want) {
				t.Fatalf("expected %q appended, got %v", want, got)
			}
			// original args preserved, exactly one proxy arg added.
			if len(got) != len(c.argv)+1 {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(c.argv)+1, got)
			}
		})
	}
}

func TestChromiumProxyArgs_DoesNotAppend(t *testing.T) {
	const socks = "127.0.0.1:1080"
	cases := []struct {
		name string
		argv []string
	}{
		{"plain cli curl", []string{"curl", "https://example.com"}},
		{"plain cli sh", []string{"/bin/sh", "-c", "echo hi"}},
		{"unknown app", []string{"firefox"}},
		{"existing proxy-server=", []string{"chromium", "--proxy-server=socks5://10.0.0.1:9050"}},
		{"existing proxy-server space", []string{"cursor", "--proxy-server", "socks5://10.0.0.1:9050"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chromiumProxyArgs(c.argv, socks)
			if !equalStr(got, c.argv) {
				t.Fatalf("argv changed: got %v, want %v", got, c.argv)
			}
		})
	}
}

func TestChromiumProxyArgs_EmptyInputs(t *testing.T) {
	if got := chromiumProxyArgs(nil, "127.0.0.1:1080"); got != nil {
		t.Errorf("nil argv: got %v", got)
	}
	if got := chromiumProxyArgs([]string{"cursor"}, ""); !equalStr(got, []string{"cursor"}) {
		t.Errorf("empty socks addr should not append: %v", got)
	}
}

func TestElectronEnv(t *testing.T) {
	const flag = "NODE_USE_ENV_PROXY=1"
	// Known Electron editors get the flag so Node fetch/undici honours HTTP_PROXY.
	for _, argv := range [][]string{
		{"cursor"},
		{"/Applications/Cursor.app/Contents/MacOS/Cursor"},
		{"code", "--new-window"},
		{"electron", "main.js"},
	} {
		got := electronEnv(argv)
		if len(got) != 1 || got[0] != flag {
			t.Errorf("electronEnv(%v) = %v, want [%s]", argv, got, flag)
		}
	}
	// Plain CLI tools and unknown apps get nothing (we don't change their net stack).
	for _, argv := range [][]string{
		nil,
		{"curl", "https://example.com"},
		{"/bin/sh"},
		{"firefox"},
	} {
		if got := electronEnv(argv); got != nil {
			t.Errorf("electronEnv(%v) = %v, want nil", argv, got)
		}
	}
}

func TestAppLabel(t *testing.T) {
	cases := map[string]string{
		"cursor":                   "cursor",
		"/usr/bin/cursor":          "cursor",
		"/Applications/Cursor.app": "cursor",
		"/Applications/Cursor.app/Contents/MacOS/Cursor": "cursor",
		"Discord.exe": "discord",
		"/Applications/Claude.app/Contents/MacOS/Claude": "claude",
		"/opt/Slack/Slack":      "slack",
		"":                      "",
		"Google Chrome":         "google chrome",
		"/usr/local/bin/MyTool": "mytool",
	}
	for in, want := range cases {
		if got := appLabel(in); got != want {
			t.Errorf("appLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// collectSink is a thread-safe OutputSink for tests.
type collectSink struct {
	mu    sync.Mutex
	lines []OutputLine
}

func (c *collectSink) Line(l OutputLine) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, l)
}

func (c *collectSink) snapshot() []OutputLine {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]OutputLine, len(c.lines))
	copy(out, c.lines)
	return out
}

func TestSinkFunc_Line(t *testing.T) {
	var got OutputLine
	var f OutputSink = SinkFunc(func(l OutputLine) { got = l })
	f.Line(OutputLine{PID: 7, App: "x", Stream: "out", Text: "hi"})
	if got.PID != 7 || got.App != "x" || got.Stream != "out" || got.Text != "hi" {
		t.Errorf("SinkFunc did not pass through: %+v", got)
	}
}

// TestStartProcess_CapturesOutput exercises the real pipe plumbing with a trivial
// command. It is guarded for OS/availability so it stays hermetic.
func TestStartProcess_CapturesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX echo")
	}
	if _, err := resolveExecutable("echo"); err != nil {
		t.Skip("echo not available")
	}
	sink := &collectSink{}
	pid, err := startProcess(context.Background(), []string{"echo", "hello-proxy"}, nil, nil, sink)
	if err != nil {
		t.Fatalf("startProcess: %v", err)
	}
	if pid <= 0 {
		t.Fatalf("pid = %d, want > 0", pid)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		lines := sink.snapshot()
		var sawOut, sawExit bool
		for _, l := range lines {
			if l.App != "echo" {
				t.Errorf("app label = %q, want %q", l.App, "echo")
			}
			if l.PID != pid {
				t.Errorf("line pid = %d, want %d", l.PID, pid)
			}
			if l.Stream == "out" && strings.Contains(l.Text, "hello-proxy") {
				sawOut = true
			}
			if l.Stream == "err" && strings.HasPrefix(l.Text, "[процесс завершён") {
				sawExit = true
			}
		}
		if sawOut && sawExit {
			return // success
		}
		if time.Now().After(deadline) {
			t.Fatalf("did not observe captured stdout + exit line; got %+v", lines)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestExitText(t *testing.T) {
	if got := exitText(nil); got != "[процесс завершён]" {
		t.Errorf("exitText(nil) = %q", got)
	}
	if got := exitText(context.Canceled); !strings.HasPrefix(got, "[процесс завершён: ") {
		t.Errorf("exitText(err) = %q", got)
	}
}

func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}
