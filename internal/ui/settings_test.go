package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func openSettings(t *testing.T, b *fakeBackend) Model {
	t.Helper()
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile().
		WithSettings(Settings{SocksPort: 1080, ClashEnabled: true, ClashAddr: "127.0.0.1:9090", SaveProfile: true})
	m.width, m.height = 100, 30
	m.relayout()
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")}) // open Настройки
	if !m.ShowingSettings() {
		t.Fatal("'g' should open the Настройки section")
	}
	return m
}

func TestSettings_EditPortAndApply(t *testing.T) {
	b := &fakeBackend{}
	m := openSettings(t, b)
	// Field 0 is SOCKS-порт; Enter to edit, type a new value, Enter to commit.
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.setForm.editing {
		t.Fatal("Enter on the port field should start editing")
	}
	m.setForm.edit.SetValue("1090")
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.setForm.editing {
		t.Error("Enter should commit the edit")
	}
	if m.DraftSettings().SocksPort != 1090 {
		t.Errorf("draft port = %d, want 1090", m.DraftSettings().SocksPort)
	}
	// Move to the "Применить" action and trigger it.
	for i := 0; i < len(settingsFields); i++ {
		if settingsFields[i].action == "apply" {
			m.setForm.focus = i
			break
		}
	}
	m, cmd := step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("apply should issue a command")
	}
	_ = cmd()
	if b.applyCalls != 1 || b.applied.SocksPort != 1090 {
		t.Errorf("ApplySettings called with port %d (calls=%d), want 1090/1", b.applied.SocksPort, b.applyCalls)
	}
}

func TestSettings_ToggleClash(t *testing.T) {
	b := &fakeBackend{}
	m := openSettings(t, b)
	m.setForm.focus = 1 // Clash API toggle
	if !m.DraftSettings().ClashEnabled {
		t.Fatal("precondition: clash enabled")
	}
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.DraftSettings().ClashEnabled {
		t.Error("Enter on the toggle should flip ClashEnabled to false")
	}
}

func TestSettings_DaemonAction(t *testing.T) {
	b := &fakeBackend{}
	m := openSettings(t, b)
	for i := 0; i < len(settingsFields); i++ {
		if settingsFields[i].action == "daemon" {
			m.setForm.focus = i
			break
		}
	}
	m, cmd := step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("daemon action should issue a command")
	}
	if !m.Busy() {
		t.Error("daemon action should set busy (drives the progress bar)")
	}
	msg := cmd()
	if _, ok := msg.(daemonizedMsg); !ok {
		t.Fatalf("expected daemonizedMsg, got %T", msg)
	}
	if b.daemonCalls != 1 {
		t.Errorf("Daemonize calls = %d, want 1", b.daemonCalls)
	}
	// A successful daemonize quits the TUI.
	_, qcmd := step(m, msg)
	if qcmd == nil || func() bool { _, ok := qcmd().(tea.QuitMsg); return !ok }() {
		t.Error("successful daemonize should quit the TUI")
	}
}

func TestSettings_AttachedShowsStopDaemon(t *testing.T) {
	b := &fakeBackend{}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile().WithAttached(4321)
	m.width, m.height = 100, 30
	m.relayout()
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	out := m.View()
	if !strings.Contains(out, "Остановить демон") {
		t.Errorf("attached settings should offer 'Остановить демон':\n%s", out)
	}
	if strings.Contains(out, "Запустить в фоне") {
		t.Error("attached settings should NOT offer the daemonize action")
	}
	// Focus the stop-daemon action and trigger it.
	fields := m.settingsFieldsFor()
	for i := range fields {
		if fields[i].action == "stopdaemon" {
			m.setForm.focus = i
		}
	}
	m, cmd := step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("stop-daemon should issue a command")
	}
	msg := cmd()
	if b.stopDaemonCalls != 1 {
		t.Errorf("StopDaemon calls = %d, want 1", b.stopDaemonCalls)
	}
	if _, qcmd := step(m, msg); qcmd == nil || func() bool { _, ok := qcmd().(tea.QuitMsg); return !ok }() {
		t.Error("successful stop-daemon should quit the client")
	}
}

func TestDashboard_AttachedBadge(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile().WithAttached(4321).WithDisplayMode(RunProxy)
	m.width, m.height = 100, 30
	m.relayout()
	out := m.View()
	if !strings.Contains(out, "attached PID 4321") {
		t.Errorf("dashboard should show the attached badge:\n%s", out)
	}
}

func TestSettingsView_RendersFields(t *testing.T) {
	m := openSettings(t, &fakeBackend{})
	out := m.View()
	for _, want := range []string{"SOCKS-порт", "Clash API", "urltest", "Применить", "Запустить в фоне"} {
		if !strings.Contains(out, want) {
			t.Errorf("settings view missing %q:\n%s", want, out)
		}
	}
}
