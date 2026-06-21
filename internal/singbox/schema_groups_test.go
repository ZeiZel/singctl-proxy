package singbox

import (
	"strings"
	"testing"
)

// These tests pin the JSON shape of the new schema additions (Clash API,
// urltest/selector groups, process route fields) independently of the full
// config goldens, so a field-tag typo is caught even before they are wired in.

func TestClashAPI_MarshalShape(t *testing.T) {
	cfg := Config{Experimental: &Experimental{
		ClashAPI: &ClashAPI{ExternalController: "127.0.0.1:9090", Secret: "s3cr3t"},
	}}
	got, err := MarshalIndented(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(got)
	for _, want := range []string{`"clash_api"`, `"external_controller": "127.0.0.1:9090"`, `"secret": "s3cr3t"`} {
		if !strings.Contains(s, want) {
			t.Errorf("clash_api JSON missing %s:\n%s", want, s)
		}
	}
}

func TestURLTestOutbound_MarshalShape(t *testing.T) {
	cfg := Config{Outbounds: []any{
		URLTestOutbound{Type: "urltest", Tag: "proxy", Outbounds: []string{"proxy-0", "proxy-1"},
			URL: "https://www.gstatic.com/generate_204", Interval: "3m", Tolerance: 50},
	}}
	got, _ := MarshalIndented(cfg)
	s := string(got)
	for _, want := range []string{`"type": "urltest"`, `"tag": "proxy"`, `"proxy-0"`, `"proxy-1"`, `"interval": "3m"`, `"tolerance": 50`} {
		if !strings.Contains(s, want) {
			t.Errorf("urltest JSON missing %s:\n%s", want, s)
		}
	}
}

func TestRouteRule_ProcessFields_MarshalShape(t *testing.T) {
	cfg := Config{Route: &Route{Rules: []RouteRule{
		{ProcessPath: []string{"__never__"}, Outbound: "proxy"},
	}}}
	got, _ := MarshalIndented(cfg)
	if !strings.Contains(string(got), `"process_path"`) {
		t.Errorf("expected process_path in JSON:\n%s", got)
	}
	// process_name must be omitted when empty (omitempty).
	if strings.Contains(string(got), `"process_name"`) {
		t.Errorf("expected process_name omitted when empty:\n%s", got)
	}
}
