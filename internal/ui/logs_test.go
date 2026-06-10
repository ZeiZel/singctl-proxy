package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestToggleLogs(t *testing.T) {
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	m, cmd := step(m, rune_("l"))
	if !m.ShowingLogs() {
		t.Fatal("l should open the logs view")
	}
	if cmd == nil {
		t.Error("opening logs should issue a read+tick command")
	}
	m, _ = step(m, rune_("l"))
	if m.ShowingLogs() {
		t.Error("l again should close the logs view")
	}
}

func TestLogsMsg_SetsContent(t *testing.T) {
	m := New(&fakeBackend{}, nil)
	m, _ = step(m, logsMsg{content: "line1\nline2"})
	if m.Logs() != "line1\nline2" {
		t.Errorf("logs = %q", m.Logs())
	}
}

func TestLogsView_RendersTail(t *testing.T) {
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	m.showLogs = true
	m.logs = "a\nb\nc"
	m, _ = step(m, tea.WindowSizeMsg{Width: 80, Height: 20})
	out := m.View()
	if !strings.Contains(out, "логи sing-box") || !strings.Contains(out, "c") {
		t.Errorf("logs view missing expected content:\n%s", out)
	}
}

func TestLogsTick_NoopWhenHidden(t *testing.T) {
	m := New(&fakeBackend{}, nil)
	_, cmd := step(m, logsTickMsg{})
	if cmd != nil {
		t.Error("logs tick should be a no-op when the logs view is hidden")
	}
}

func TestEditKey_ClearsFieldAndReturnsToLink(t *testing.T) {
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	m.input.SetValue("vless://old@h:1")
	m, _ = step(m, rune_("e"))
	if m.Screen() != ScreenLink {
		t.Error("e should return to the link screen")
	}
	if m.LinkValue() != "" {
		t.Errorf("link field should be cleared for a new link, got %q", m.LinkValue())
	}
}

func TestEsc_FromEdit_ReturnsToDashboard(t *testing.T) {
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	m, _ = step(m, rune_("e")) // go to edit
	if m.Screen() != ScreenLink {
		t.Fatal("precondition: should be on link screen")
	}
	m, cmd := step(m, escKey)
	if m.Screen() != ScreenDashboard {
		t.Error("esc should return to the dashboard when a profile is loaded")
	}
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Error("esc must not quit when a profile is loaded")
		}
	}
}

func TestEsc_FirstRun_Quits(t *testing.T) {
	m := New(&fakeBackend{}, nil) // not loaded, first-run link screen
	_, cmd := step(m, escKey)
	if cmd == nil {
		t.Fatal("esc on first-run screen should issue a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("esc on first-run screen (no profile) should quit")
	}
}

func TestWithLoadedProfile_StartsOnDashboardOff(t *testing.T) {
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	if m.Screen() != ScreenDashboard {
		t.Error("a loaded profile should start on the dashboard, skipping input")
	}
	if m.Mode() != RunOff {
		t.Error("must start OFF — user picks the mode explicitly")
	}
}

func TestLogsView_LongLogsAreTailed(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString("logline\n")
	}
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	m.showLogs = true
	m.logs = sb.String()
	m, _ = step(m, tea.WindowSizeMsg{Width: 80, Height: 20})
	out := m.View()
	if strings.Count(out, "logline") > 30 {
		t.Errorf("logs not tailed to fit the screen: %d lines", strings.Count(out, "logline"))
	}
}
