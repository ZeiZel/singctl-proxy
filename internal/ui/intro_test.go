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

func TestIntro_BouncingBallMoves(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile().WithIntro(true)
	m, _ = step(m, tea.WindowSizeMsg{Width: 80, Height: 28})
	col := func() int {
		line := m.bouncingBall(80)
		for _, l := range splitLines(line) {
			for i, r := range l {
				if r == 'o' || r == '●' {
					return i
				}
			}
		}
		return -1
	}
	m.introFrame = 0
	a := col()
	m.introFrame = 10
	b := col()
	if a == b {
		t.Errorf("the ball should move between frames (frame0=%d frame10=%d)", a, b)
	}
	if a < 0 || b < 0 {
		t.Errorf("the ball should be rendered (cols %d,%d)", a, b)
	}
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(r)
		}
	}
	return append(out, cur)
}
