package main

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

func mustParse(t *testing.T, args ...string) *options {
	t.Helper()
	o, err := parseOptions(args, io.Discard)
	if err != nil {
		t.Fatalf("parseOptions(%v): %v", args, err)
	}
	return o
}

func TestParseOptions_ShortAndLongForms(t *testing.T) {
	o := mustParse(t, "-k", "vless://x@y:1", "-p", "-l", "--port", "7890")
	if o.combinedKey() != "vless://x@y:1" || !o.proxy || !o.logs || o.port != 7890 {
		t.Errorf("short forms not parsed: %+v", o)
	}
	o = mustParse(t, "--key", "vless://a@b:2", "--proxy", "--logs", "--headless", "--no-save")
	if o.combinedKey() != "vless://a@b:2" || !o.proxy || !o.logs || !o.headless || !o.noSave {
		t.Errorf("long forms not parsed: %+v", o)
	}
}

func TestParseOptions_RepeatableKeysForFailover(t *testing.T) {
	o := mustParse(t, "-k", "vless://a@h:1", "-k", "vless://b@h:2", "--key", "vless://c@h:3")
	if len(o.keys) != 3 {
		t.Fatalf("expected 3 keys, got %d: %v", len(o.keys), o.keys)
	}
	want := "vless://a@h:1\nvless://b@h:2\nvless://c@h:3"
	if o.combinedKey() != want {
		t.Errorf("combinedKey() = %q, want priority-ordered blob %q", o.combinedKey(), want)
	}
}

func TestClashAPI_DefaultsAndDisable(t *testing.T) {
	o := mustParse(t)
	if o.effectiveClashAPI() != defaultClashAPI {
		t.Errorf("clash API should default to %q, got %q", defaultClashAPI, o.effectiveClashAPI())
	}
	o = mustParse(t, "--no-clash-api")
	if o.effectiveClashAPI() != "" {
		t.Errorf("--no-clash-api must disable the Clash API, got %q", o.effectiveClashAPI())
	}
	o = mustParse(t, "--clash-api", "127.0.0.1:9091")
	if o.effectiveClashAPI() != "127.0.0.1:9091" {
		t.Errorf("--clash-api override failed: %q", o.effectiveClashAPI())
	}
}

func TestParseOptions_HelpAndErrors(t *testing.T) {
	if _, err := parseOptions([]string{"-h"}, io.Discard); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("-h must return flag.ErrHelp, got %v", err)
	}
	if _, err := parseOptions([]string{"--vpn", "--proxy"}, io.Discard); err == nil {
		t.Error("--vpn with --proxy must be rejected")
	}
	if _, err := parseOptions([]string{"--port", "65535"}, io.Discard); err == nil {
		t.Error("port 65535 must be rejected (http listener needs port+1)")
	}
	if _, err := parseOptions([]string{"stray"}, io.Discard); err == nil {
		t.Error("positional arguments must be rejected")
	}
}

func TestApplyEnv_FillsMissingAndFlagsWin(t *testing.T) {
	env := map[string]string{envKey: "vless://env@h:1", envPort: "9000"}
	getenv := func(k string) string { return env[k] }

	o := mustParse(t)
	if err := o.applyEnv(getenv); err != nil {
		t.Fatal(err)
	}
	if o.combinedKey() != "vless://env@h:1" || o.port != 9000 {
		t.Errorf("env not applied: key=%q port=%d", o.combinedKey(), o.port)
	}

	o = mustParse(t, "-k", "vless://flag@h:1", "--port", "7000")
	if err := o.applyEnv(getenv); err != nil {
		t.Fatal(err)
	}
	if o.combinedKey() != "vless://flag@h:1" || o.port != 7000 {
		t.Errorf("flags must win over env: key=%q port=%d", o.combinedKey(), o.port)
	}

	env[envPort] = "not-a-number"
	o = mustParse(t)
	if err := o.applyEnv(getenv); err == nil {
		t.Error("invalid SINGCTL_PORT must be rejected")
	}
}

func TestUsage_MentionsEveryFlag(t *testing.T) {
	for _, f := range []string{"--key", "--headless", "--logs", "--vpn", "--proxy", "--port", "--clash-api", "--no-clash-api", "--clash-secret", "--urltest-url", "--urltest-interval", "--urltest-tolerance", "--env-file", "--no-save", "--man", "--version", "--help", envKey, envKeys, envPort, envClashAPI, envClashSecret} {
		if !strings.Contains(usageText, f) {
			t.Errorf("usage text misses %s", f)
		}
	}
}
