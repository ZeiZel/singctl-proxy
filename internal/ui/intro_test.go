package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestIntro_FullRunMarksSeenThenReveals(t *testing.T) {
	b := &fakeBackend{}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile().WithIntro(true)
	m, _ = step(m, tea.WindowSizeMsg{Width: 90, Height: 30})
	if m.Screen() != ScreenIntro {
		t.Fatalf("should start on the intro screen, got %v", m.Screen())
	}
	// Tick through every component stage.
	var cmd tea.Cmd
	for i := 0; i < len(introStages); i++ {
		m, cmd = step(m, introTickMsg{})
	}
	if m.Screen() != ScreenDashboard {
		t.Errorf("intro should reveal the dashboard (postIntro), got %v", m.Screen())
	}
	if cmd == nil {
		t.Fatal("finishing a first-run intro should issue MarkIntroSeen")
	}
	_ = cmd()
	if b.introSeenCall != 1 {
		t.Errorf("MarkIntroSeen calls = %d, want 1", b.introSeenCall)
	}
}

func TestIntro_KeySkips(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile().WithIntro(false)
	m, _ = step(m, tea.WindowSizeMsg{Width: 90, Height: 30})
	m, _ = step(m, enterKey)
	if m.Screen() != ScreenDashboard {
		t.Errorf("any key should skip the intro to the dashboard, got %v", m.Screen())
	}
}

func TestIntro_ShortRunDoesNotMark(t *testing.T) {
	b := &fakeBackend{}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile().WithIntro(false)
	m, _ = step(m, tea.WindowSizeMsg{Width: 90, Height: 30})
	for i := 0; i < len(introStages); i++ {
		m, _ = step(m, introTickMsg{})
	}
	if b.introSeenCall != 0 {
		t.Errorf("a non-first-run intro must not write the marker, calls=%d", b.introSeenCall)
	}
}
