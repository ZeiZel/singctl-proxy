package daemon

import (
	"strings"
	"testing"
)

func argString(a []string) string { return " " + strings.Join(a, " ") + " " }

func TestBuildArgs_ProxyDefaults(t *testing.T) {
	a := argString(BuildArgs(Config{Mode: "proxy"}))
	if !strings.Contains(a, " --headless ") || !strings.Contains(a, " --proxy ") {
		t.Errorf("expected headless+proxy, got %q", a)
	}
	if strings.Contains(a, "--key") {
		t.Error("daemon argv must NOT contain --key (child loads the saved profile)")
	}
}

func TestBuildArgs_VPNWithSettings(t *testing.T) {
	a := argString(BuildArgs(Config{
		Mode: "vpn", Port: 1090, ClashAddr: "127.0.0.1:9091", ClashSecret: "s",
		URLTestURL: "https://x", URLTestInterval: "5m", URLTestTolerance: 100,
	}))
	for _, want := range []string{
		" --vpn ", " --port 1090 ", " --clash-api 127.0.0.1:9091 ", " --clash-secret s ",
		" --urltest-url https://x ", " --urltest-interval 5m ", " --urltest-tolerance 100 ",
	} {
		if !strings.Contains(a, want) {
			t.Errorf("missing %q in %q", want, a)
		}
	}
}

func TestBuildArgs_NoClash(t *testing.T) {
	a := argString(BuildArgs(Config{Mode: "proxy", NoClash: true, ClashAddr: "127.0.0.1:9090"}))
	if !strings.Contains(a, " --no-clash-api ") {
		t.Errorf("expected --no-clash-api, got %q", a)
	}
	if strings.Contains(a, "--clash-api") {
		t.Error("--no-clash-api must suppress --clash-api")
	}
}
