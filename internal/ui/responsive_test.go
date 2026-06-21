package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// asciiModel builds a model with deterministic (ascii) capabilities so rendered
// width never depends on the host locale/terminal.
func asciiModel() Model {
	return newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
}

// widthOK reports the first line that exceeds w cells, if any.
func widthOK(out string, w int) (string, bool) {
	for _, ln := range strings.Split(out, "\n") {
		if lipgloss.Width(ln) > w {
			return ln, false
		}
	}
	return "", true
}

// TestView_NeverExceedsWidth is the core responsive invariant: across a wide
// range of terminal sizes and on every screen, no rendered line is wider than
// the terminal. This is what guarantees the Cisco warning (and everything else)
// can never overflow a narrow terminal.
func TestView_NeverExceedsWidth(t *testing.T) {
	type scene struct {
		name  string
		setup func(Model) Model
	}
	scenes := []scene{
		{"dashboard-off", func(m Model) Model { return m }},
		{"dashboard-proxy", func(m Model) Model { m.mode = RunProxy; m.status = "PROXY запущен"; return m }},
		{"dashboard-vpn", func(m Model) Model { m.mode = RunVPN; return m }},
		{"dashboard-cisco", func(m Model) Model { m.cisco = true; m.phys = "en0"; return m }},
		{"dashboard-busy", func(m Model) Model { m.busy = true; m.status = "запуск PROXY…"; return m }},
		{"dashboard-conns", func(m Model) Model {
			m.mode = RunProxy
			for i := 0; i < 12; i++ {
				m.conns = append(m.conns, ConnRow{Process: "codex", Source: "127.0.0.1:54321",
					Dest: "api.openai.com:443", Network: "tcp", Chain: "proxy-0"})
			}
			m.latency = []LatencyRow{{Tag: "proxy-0", Delay: 42, Selected: true}, {Tag: "proxy-1", Delay: 88}}
			m.latencySel = "proxy-0"
			return m
		}},
		{"dashboard-err", func(m Model) Model {
			m.errText = "не удалось разобрать ссылку: неизвестный протокол в очень длинном сообщении"
			return m
		}},
		{"link", func(m Model) Model {
			m.screen = ScreenLink
			m.input.Focus()
			m.input.SetValue("vless://uuid@example.com:443?sni=a.very.long.host.name")
			return m
		}},
		{"link-err", func(m Model) Model {
			m.screen = ScreenLink
			m.input.Focus()
			m.errText = "введите vless:// ссылку"
			return m
		}},
		{"apps", func(m Model) Model {
			m.showProc = true
			m.procRows = []ProcInfo{{PID: 123, Name: "zen", Ports: ":443"}, {PID: 456, Name: "codex"}}
			m.routedPIDs = []int{123}
			return m
		}},
		{"settings", func(m Model) Model {
			m.showSettings = true
			m.setForm.draft = Settings{SocksPort: 1080, ClashEnabled: true, ClashAddr: "127.0.0.1:9090", URLTestInterval: "3m"}
			return m
		}},
		{"logs", func(m Model) Model {
			m.showLogs = true
			var sb strings.Builder
			for i := 0; i < 200; i++ {
				sb.WriteString("2026-06-02 10:00:01 inbound/mixed started a fairly long log line to force wrapping behaviour\n")
			}
			m.logs = sb.String()
			return m
		}},
		{"modal", func(m Model) Model {
			m.modal = "Cisco Secure Client активен — VPN-режим заблокирован, чтобы не конфликтовать ни единым пакетом. Отключите Cisco и повторите."
			return m
		}},
	}

	for w := 12; w <= 120; w += 2 {
		for _, h := range []int{8, 10, 14, 18, 24, 40} {
			for _, sc := range scenes {
				m := sc.setup(asciiModel())
				m, _ = step(m, tea.WindowSizeMsg{Width: w, Height: h})
				out := m.View()
				if bad, ok := widthOK(out, w); !ok {
					t.Fatalf("%s @ %dx%d: line exceeds width %d (got %d):\n%q",
						sc.name, w, h, w, lipgloss.Width(bad), bad)
				}
			}
		}
	}
}

func TestModal_FitsNarrowTerminal(t *testing.T) {
	for _, w := range []int{20, 30, 40} {
		m := asciiModel()
		m.modal = "Cisco Secure Client активен — VPN-режим заблокирован. Отключите Cisco и повторите."
		m, _ = step(m, tea.WindowSizeMsg{Width: w, Height: 18})
		out := m.View()
		if !strings.Contains(out, "Cisco") {
			t.Errorf("modal @ w=%d should mention Cisco:\n%s", w, out)
		}
		if bad, ok := widthOK(out, w); !ok {
			t.Errorf("modal @ w=%d overflows: %q", w, bad)
		}
	}
}

func TestTooSmall_Fallback(t *testing.T) {
	m := asciiModel()
	m, _ = step(m, tea.WindowSizeMsg{Width: 20, Height: 6})
	out := m.View()
	if !strings.Contains(out, "маленькое") {
		t.Errorf("expected too-small hint below the floor, got:\n%s", out)
	}
	// At/above the floor the real dashboard renders.
	m, _ = step(m, tea.WindowSizeMsg{Width: 70, Height: 24})
	if out := m.View(); !strings.Contains(out, "СТАТУС") {
		t.Errorf("expected the dashboard at/above the floor, got:\n%s", out)
	}
}

func TestSelector_EnterAppliesMode(t *testing.T) {
	b := &fakeBackend{}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	// Cursor → PROXY (→ key; focus defaults to the selector), then Enter applies.
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.segCursor != 1 {
		t.Fatalf("right should move cursor to PROXY, got %d", m.segCursor)
	}
	m, cmd := step(m, enterKey)
	if cmd == nil {
		t.Fatal("enter on PROXY should issue a command")
	}
	m, _ = step(m, cmd())
	if m.Mode() != RunProxy {
		t.Errorf("enter on PROXY segment should start proxy, mode=%v", m.Mode())
	}
}

func TestSelector_VPNViaEnter_BlockedByCisco(t *testing.T) {
	b := &fakeBackend{}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.cisco = true
	m.segCursor = 2 // VPN
	m, cmd := step(m, enterKey)
	if !m.ModalShown() {
		t.Error("activating VPN via Enter while Cisco active must show the modal")
	}
	if cmd != nil {
		t.Error("must not issue an enable command while Cisco active")
	}
	if b.vpnCalls != 0 {
		t.Error("EnableVPN must not be called")
	}
}

func TestHelpFooter_Present(t *testing.T) {
	m := asciiModel()
	m, _ = step(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	out := m.View()
	if !strings.Contains(out, "прокси") || !strings.Contains(out, "выход") {
		t.Errorf("wide dashboard footer should list key hints, got:\n%s", out)
	}
}

func TestHelp_ToggleFullHelp(t *testing.T) {
	m := asciiModel()
	m, _ = step(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	if m.help.ShowAll {
		t.Fatal("help should start collapsed")
	}
	m, _ = step(m, rune_("?"))
	if !m.help.ShowAll {
		t.Error("? should expand full help")
	}
}
