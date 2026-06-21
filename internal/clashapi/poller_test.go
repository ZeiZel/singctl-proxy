package clashapi

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFormatLine(t *testing.T) {
	now := time.Date(2026, 6, 21, 15, 4, 5, 0, time.UTC)
	c := Connection{
		Metadata: Metadata{Network: "tcp", SourceIP: "127.0.0.1", SourcePort: "54321",
			Host: "api.openai.com", DestinationPort: "443", Process: "codex"},
		Chains: []string{"proxy-0"},
	}
	got := FormatLine(now, c)
	want := "15:04:05  codex  127.0.0.1:54321 → api.openai.com:443  [tcp]  via proxy-0"
	if got != want {
		t.Errorf("FormatLine()\n got: %q\nwant: %q", got, want)
	}
}

func TestFormatLine_UnknownProcess(t *testing.T) {
	c := Connection{Metadata: Metadata{Network: "udp", SourceIP: "127.0.0.1", SourcePort: "1",
		DestinationIP: "8.8.8.8", DestinationPort: "53"}}
	got := FormatLine(time.Now(), c)
	if !strings.Contains(got, "  ?  ") {
		t.Errorf("expected unknown process marker '?', got %q", got)
	}
	if !strings.Contains(got, "8.8.8.8:53") {
		t.Errorf("expected ip dest fallback, got %q", got)
	}
}

func TestPoller_EnrichesAndLogsOnce(t *testing.T) {
	c, srv := newTestServer(t, "")
	defer srv.Close()

	var lines []string
	var lastConns []Connection
	p := &Poller{
		Client:   c,
		Interval: 10 * time.Millisecond,
		Resolve:  func(int) string { return "resolved" }, // process already set, must NOT override
		Sink: Sink{
			LogLine:     func(s string) { lines = append(lines, s) },
			Connections: func(cs []Connection) { lastConns = cs },
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	p.Run(ctx)

	if len(lines) != 1 {
		t.Errorf("expected exactly 1 logged line (deduped by id), got %d: %v", len(lines), lines)
	}
	if len(lastConns) != 1 {
		t.Fatalf("expected 1 connection pushed, got %d", len(lastConns))
	}
	if lastConns[0].Metadata.Process != "codex" {
		t.Errorf("resolver must not override an existing process name, got %q", lastConns[0].Metadata.Process)
	}
}

func TestPoller_ResolverFillsEmptyProcess(t *testing.T) {
	p := &Poller{Resolve: func(port int) string {
		if port == 54321 {
			return "filled"
		}
		return ""
	}}
	conns := []Connection{{Metadata: Metadata{SourcePort: "54321"}}}
	p.enrich(conns)
	if conns[0].Metadata.Process != "filled" {
		t.Errorf("enrich should fill empty process, got %q", conns[0].Metadata.Process)
	}
}
