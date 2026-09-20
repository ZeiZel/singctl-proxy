package main

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"

	"singctl/internal/control"
	"singctl/internal/protocol/all"
	"singctl/internal/sub"
)

// testProtoReg is the real, default protocol registry — the same one
// production code builds from all.Registry().
var testProtoReg = all.Registry()

func mustParse(t *testing.T, args ...string) *cli {
	t.Helper()
	c, err := parseCLI(args, io.Discard, testProtoReg)
	if err != nil {
		t.Fatalf("parseCLI(%v): %v", args, err)
	}
	return c
}

func TestParseOptions_ShortAndLongForms(t *testing.T) {
	c := mustParse(t, "-k", "vless://x@y:1", "-p", "-l", "--port", "7890")
	if c.keys.combined() != "vless://x@y:1" || !c.proxy.proxy || !c.proxy.logs || c.proxy.port != 7890 {
		t.Errorf("short forms not parsed: %+v", c)
	}
	c = mustParse(t, "--key", "vless://a@b:2", "--proxy", "--logs", "--headless", "--no-save")
	if c.keys.combined() != "vless://a@b:2" || !c.proxy.proxy || !c.proxy.logs || !c.proxy.headless || !c.keys.noSave {
		t.Errorf("long forms not parsed: %+v", c)
	}
}

func TestParseOptions_RepeatableKeysForFailover(t *testing.T) {
	c := mustParse(t, "-k", "vless://a@h:1", "-k", "vless://b@h:2", "--key", "vless://c@h:3")
	if len(c.keys.keys) != 3 {
		t.Fatalf("expected 3 keys, got %d: %v", len(c.keys.keys), c.keys.keys)
	}
	want := "vless://a@h:1\nvless://b@h:2\nvless://c@h:3"
	if c.keys.combined() != want {
		t.Errorf("combined() = %q, want priority-ordered blob %q", c.keys.combined(), want)
	}
}

func TestClashAPI_DefaultsAndDisable(t *testing.T) {
	c := mustParse(t)
	if c.obs.effectiveClashAPI() != defaultClashAPI {
		t.Errorf("clash API should default to %q, got %q", defaultClashAPI, c.obs.effectiveClashAPI())
	}
	c = mustParse(t, "--no-clash-api")
	if c.obs.effectiveClashAPI() != "" {
		t.Errorf("--no-clash-api must disable the Clash API, got %q", c.obs.effectiveClashAPI())
	}
	c = mustParse(t, "--clash-api", "127.0.0.1:9091")
	if c.obs.effectiveClashAPI() != "127.0.0.1:9091" {
		t.Errorf("--clash-api override failed: %q", c.obs.effectiveClashAPI())
	}
}

func TestParseOptions_HelpAndErrors(t *testing.T) {
	if _, err := parseCLI([]string{"-h"}, io.Discard, testProtoReg); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h must return flag.ErrHelp, got %v", err)
	}
	if _, err := parseCLI([]string{"--vpn", "--proxy"}, io.Discard, testProtoReg); err == nil {
		t.Error("--vpn with --proxy must be rejected")
	}
	if _, err := parseCLI([]string{"--port", "65535"}, io.Discard, testProtoReg); err == nil {
		t.Error("port 65535 must be rejected (http listener needs port+1)")
	}
	if _, err := parseCLI([]string{"stray"}, io.Discard, testProtoReg); err == nil {
		t.Error("positional arguments must be rejected")
	}
	if _, err := parseCLI([]string{"--launch"}, io.Discard, testProtoReg); err == nil {
		t.Error("--launch without a command must be rejected")
	}
}

func TestApplyEnv_FillsMissingAndFlagsWin(t *testing.T) {
	env := map[string]string{envKey: "vless://env@h:1", envPort: "9000"}
	getenv := func(k string) string { return env[k] }

	c := mustParse(t)
	if err := c.applyEnv(getenv); err != nil {
		t.Fatal(err)
	}
	if c.keys.combined() != "vless://env@h:1" || c.proxy.port != 9000 {
		t.Errorf("env not applied: key=%q port=%d", c.keys.combined(), c.proxy.port)
	}

	c = mustParse(t, "-k", "vless://flag@h:1", "--port", "7000")
	if err := c.applyEnv(getenv); err != nil {
		t.Fatal(err)
	}
	if c.keys.combined() != "vless://flag@h:1" || c.proxy.port != 7000 {
		t.Errorf("flags must win over env: key=%q port=%d", c.keys.combined(), c.proxy.port)
	}

	env[envPort] = "not-a-number"
	c = mustParse(t)
	if err := c.applyEnv(getenv); err == nil {
		t.Error("invalid SINGCTL_PORT must be rejected")
	}
}

// TestFlagDescriptorParity is the structural replacement for the old usage-string
// scan: every bound flag must be documented by a descriptor and vice versa.
func TestFlagDescriptorParity(t *testing.T) {
	c := &cli{}
	c.buildRegistry(testProtoReg)
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	c.reg.Bind(fs)

	bound := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { bound[f.Name] = true })
	declared := c.reg.FlagNames()

	for name := range bound {
		if !declared[name] {
			t.Errorf("flag --%s is bound but not documented in any Descriptor", name)
		}
	}
	for name := range declared {
		if !bound[name] {
			t.Errorf("flag --%s is documented but not bound", name)
		}
	}
}

func TestHelpGenerated_ListsFlags(t *testing.T) {
	c := &cli{}
	c.buildRegistry(testProtoReg)
	var b strings.Builder
	c.reg.Help(&b)
	out := b.String()
	for _, want := range []string{
		"sudo singctl [flags]", "-k, --key", "--clash-api", "--route-pid",
		"--attach", "Environment:", "SINGCTL_KEY", "SINGCTL_CLASH_API",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated help missing %q", want)
		}
	}
}

// TestManPageParity guards the embedded singctl.1 against flag drift: every long
// flag in the registry must be documented in the man page.
func TestManPageParity(t *testing.T) {
	c := &cli{}
	c.buildRegistry(testProtoReg)
	manClean := strings.ReplaceAll(manPage, `\-`, "-") // undo roff dash escaping
	for name := range c.reg.FlagNames() {
		if len(name) == 1 {
			continue // short synonyms documented under their long form
		}
		if !strings.Contains(manClean, "--"+name) {
			t.Errorf("man page (singctl.1) does not document --%s", name)
		}
	}
}

// TestControlDeadlineCoversSubscriptionFetch pins a relationship that is easy
// to break from either side and fails in a maddening way when broken: the
// control socket's SUB-*/SYSPROXY-* deadline must exceed the slowest
// handler's own budget.
//
// At 15s it silently cut every subscription command short — a subscription
// fetch costs up to two AttemptTimeouts (direct, then via the local proxy) —
// so an unreachable panel reached the UI as "control socket connect failed"
// instead of the real "the panel did not answer". Lowering either constant
// past the other brings that back.
func TestControlDeadlineCoversSubscriptionFetch(t *testing.T) {
	fetchBudget := 2 * sub.AttemptTimeout
	if control.SlowDeadline <= fetchBudget {
		t.Fatalf("control.SlowDeadline (%v) must exceed a full subscription fetch (%v); "+
			"otherwise the daemon kills the connection mid-command and the client "+
			"reports a socket error instead of the real failure",
			control.SlowDeadline, fetchBudget)
	}
	if control.DeadlineFor("SUB-UPDATE") != control.SlowDeadline {
		t.Errorf("SUB-UPDATE deadline = %v, want SlowDeadline (%v)", control.DeadlineFor("SUB-UPDATE"), control.SlowDeadline)
	}
}

// TestControlDeadline_FastCommandsNeverGetTheLongOne is the other half of the
// relationship above: a fast command (MODE, STATUS, KEYS-*, PROXY-*, or
// anything else not explicitly a SUB-*/SYSPROXY-* command) must NOT inherit
// the long subscription-sized deadline — that was exactly the bug (F2 item
// 5): every command shared ConnDeadline, so a stuck MODE command turned a
// should-be-~15s failure into a multi-minute hang for whoever was waiting on
// the reply, and wedged the connection for that long even when the daemon
// itself recovered quickly.
func TestControlDeadline_FastCommandsNeverGetTheLongOne(t *testing.T) {
	fast := []string{"MODE", "mode", "STATUS", "KEYS-GET", "KEYS-ADD", "KEYS-REMOVE",
		"KEYS-RENAME", "PROXY-GROUP", "PROXY-SELECT", "STOP", "SETTINGS-GET", "SETTINGS-SET"}
	for _, cmd := range fast {
		if got := control.DeadlineFor(cmd); got != control.FastDeadline {
			t.Errorf("DeadlineFor(%q) = %v, want FastDeadline (%v)", cmd, got, control.FastDeadline)
		}
	}
	slow := []string{"SUB-LIST", "SUB-ADD", "SUB-REMOVE", "SUB-UPDATE",
		"SYSPROXY-STATUS", "SYSPROXY-SET", "SYSPROXY-CONFIG", "SYSPROXY-IMPORT"}
	for _, cmd := range slow {
		if got := control.DeadlineFor(cmd); got != control.SlowDeadline {
			t.Errorf("DeadlineFor(%q) = %v, want SlowDeadline (%v)", cmd, got, control.SlowDeadline)
		}
	}
	if control.FastDeadline >= control.SlowDeadline {
		t.Fatalf("FastDeadline (%v) must be strictly shorter than SlowDeadline (%v)", control.FastDeadline, control.SlowDeadline)
	}
}
