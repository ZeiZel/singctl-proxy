package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAppsView_RendersLauncherAndRoutedList(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m.width, m.height = 100, 30
	m.relayout()
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}) // open Приложения
	m.routedPIDs = []int{4242}
	out := m.View()
	for _, want := range []string{"Запустить приложение в прокси", "Проксировать запущенный процесс", "Проксируются сейчас", "4242"} {
		if !strings.Contains(out, want) {
			t.Errorf("apps view missing %q:\n%s", want, out)
		}
	}
}

// The picker list must be navigable with ↑/↓ regardless of which field is
// focused — the user's complaint was that selection only worked on the second
// input. Here ↓ moves the cursor while the launch field (appFocus 0) is focused.
func TestApps_ListNavigableFromLaunchField(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, rune_("x")) // open Приложения; launch field focused (appFocus 0)
	m.procRows = []ProcInfo{{PID: 1, Name: "a"}, {PID: 2, Name: "b"}, {PID: 3, Name: "c"}}
	if m.appFocus != 0 {
		t.Fatalf("precondition: launch field focused, appFocus=%d", m.appFocus)
	}
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.procCursor != 2 {
		t.Errorf("↓ should move the picker cursor from the launch field too, got %d", m.procCursor)
	}
}

// With the launch field focused but EMPTY, Enter routes the highlighted process
// (the list is always actionable), instead of doing nothing.
func TestApps_EnterRoutesHighlightedWhenLaunchEmpty(t *testing.T) {
	b := &fakeBackend{}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, rune_("x"))
	m.procRows = []ProcInfo{{PID: 11, Name: "a"}, {PID: 22, Name: "b"}}
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyDown}) // highlight PID 22
	m, cmd := step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter with an empty launch field should route the highlighted process")
	}
	m, _ = step(m, cmd())
	if b.routedPID != 22 {
		t.Errorf("should route the highlighted PID 22, got %d", b.routedPID)
	}
}

func TestAppsView_LaunchTracksRoutedPID(t *testing.T) {
	b := &fakeBackend{launchPID: 777}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m.launchInput.SetValue("zen")
	m, cmd := step(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected launch command")
	}
	m, _ = step(m, cmd()) // procResultMsg{pid:777}
	if got := m.RoutedPIDs(); len(got) != 1 || got[0] != 777 {
		t.Errorf("RoutedPIDs = %v, want [777]", got)
	}
}
