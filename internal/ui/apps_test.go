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