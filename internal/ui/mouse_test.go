package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// A left click before any View()/Scan has populated zone bounds must not panic
// (zm.Get returns nil for unknown zones; InBounds is nil-safe) and must leave
// the mode untouched.
func TestMouse_ClickWithoutZonesIsNoop(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 90, Height: 28})
	before := m.Mode()
	m, cmd := step(m, tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      1000, // far outside any zone
		Y:      1000,
	})
	if m.Mode() != before {
		t.Errorf("click outside zones changed mode: %v -> %v", before, m.Mode())
	}
	if cmd != nil {
		t.Error("no-op click should not issue a command")
	}
}

// A non-left, non-wheel mouse event (e.g. motion) is ignored without panic.
func TestMouse_MotionIgnored(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 90, Height: 28})
	if _, cmd := step(m, tea.MouseMsg{Action: tea.MouseActionMotion}); cmd != nil {
		t.Error("motion should be ignored (no command)")
	}
}

// Wheel events while the logs overlay is open are forwarded to the viewport and
// do not panic.
func TestMouse_WheelInLogsScrolls(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile().WithLogsOpen()
	m, _ = step(m, tea.WindowSizeMsg{Width: 90, Height: 28})
	if !m.ShowingLogs() {
		t.Fatal("expected logs overlay open")
	}
	// Should not panic regardless of viewport content.
	step(m, tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	step(m, tea.MouseMsg{Button: tea.MouseButtonWheelUp})
}
