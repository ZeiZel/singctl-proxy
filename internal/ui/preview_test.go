package ui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestPreview renders the screens at several sizes so a human can eyeball the
// layout. It is a no-op unless SINGCTL_PREVIEW is set:
//
//	SINGCTL_PREVIEW=1 go test ./internal/ui/ -run TestPreview -v
//
// Unicode borders, colour stripped (ascii profile) so the captured output stays
// readable.
func TestPreview(t *testing.T) {
	if os.Getenv("SINGCTL_PREVIEW") == "" {
		t.Skip("set SINGCTL_PREVIEW=1 to render previews")
	}
	r := lipgloss.NewRenderer(os.Stdout)
	r.SetColorProfile(termenv.Ascii)
	caps := Caps{R: r, Unicode: true, Color: false}

	render := func(title string, w, h int, prep func(Model) Model) {
		m := prep(newWithCaps(&fakeBackend{}, nil, caps).WithLoadedProfile())
		m, _ = step(m, tea.WindowSizeMsg{Width: w, Height: h})
		out := m.View()
		fmt.Printf("\n### %s (%dx%d)\n", title, w, h)
		fmt.Println(strings.Repeat("=", w))
		fmt.Println(out)
		fmt.Println(strings.Repeat("=", w))
	}

	render("dashboard narrow", 40, 20, func(m Model) Model { m.phys = "en0"; return m })
	render("dashboard medium", 70, 22, func(m Model) Model { m.phys = "en0"; m.mode = RunProxy; m.status = "PROXY запущен"; return m })
	render("dashboard wide", 104, 26, func(m Model) Model { m.phys = "en0"; m.mode = RunVPN; m.status = "VPN запущен"; return m })
	render("dashboard cisco active", 70, 22, func(m Model) Model { m.phys = "en0"; m.cisco = true; m.segCursor = 2; return m })
	render("dashboard compact height", 80, 14, func(m Model) Model { m.phys = "en0"; m.mode = RunProxy; return m })
	render("link input", 70, 16, func(m Model) Model { m.screen = ScreenLink; m.input.Focus(); return m })
	render("logs", 80, 18, func(m Model) Model {
		m.showLogs = true
		var sb strings.Builder
		for i := 1; i <= 40; i++ {
			fmt.Fprintf(&sb, "2026-06-02 10:00:%02d outbound/vless-out connected to 193.188.22.147:443\n", i)
		}
		m.logs = sb.String()
		return m
	})
	render("cisco modal narrow", 38, 14, func(m Model) Model {
		m.modal = "Cisco Secure Client активен — VPN-режим заблокирован, чтобы не конфликтовать ни единым пакетом. Отключите Cisco и повторите."
		return m
	})
}
