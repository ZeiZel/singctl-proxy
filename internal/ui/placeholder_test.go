package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The edit screen ('e') must show the currently loaded link as the input
// placeholder so the user sees what they are replacing.
func TestEdit_ShowsCurrentLinkAsPlaceholder(t *testing.T) {
	const link = "vless://uuid@host:443"
	m := New(&fakeBackend{}, nil).WithLoadedProfile().WithCurrentLink(link)

	m, _ = step(m, rune_("e"))
	if m.Screen() != ScreenLink {
		t.Fatal("'e' should open the link screen")
	}
	if m.input.Value() != "" {
		t.Error("input must start empty when editing")
	}
	if m.input.Placeholder != link {
		t.Errorf("placeholder = %q, want the current link %q", m.input.Placeholder, link)
	}
}

// Without a loaded link the placeholder stays the default hint.
func TestEdit_NoLink_KeepsDefaultPlaceholder(t *testing.T) {
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	m, _ = step(m, rune_("e"))
	if m.input.Placeholder != "vless://..." {
		t.Errorf("placeholder = %q, want the default hint", m.input.Placeholder)
	}
}

// A link submitted through the input becomes the new current link (and thus
// the next edit placeholder).
func TestLinkLoaded_RemembersCurrentLink(t *testing.T) {
	const link = "vless://new@host:443"
	m := New(&fakeBackend{}, nil)
	m.input.SetValue(link)
	m, _ = step(m, linkLoadedMsg{})
	m, _ = step(m, rune_("e"))
	if m.input.Placeholder != link {
		t.Errorf("placeholder = %q, want the just-loaded link %q", m.input.Placeholder, link)
	}
}

// --proxy / --vpn must issue the enable command from Init and reflect the
// pending state.
func TestAutoMode_InitIssuesEnable(t *testing.T) {
	b := &fakeBackend{}
	m := New(b, nil).WithLoadedProfile().WithAutoMode(RunProxy)
	if !m.Busy() {
		t.Error("auto mode should mark the model busy")
	}
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init must return commands")
	} else {
		drain(cmd)
	}
	if b.proxyCalls != 1 {
		t.Errorf("EnableProxy calls = %d, want 1", b.proxyCalls)
	}
}

// drain executes a (possibly batched) command tree, ignoring the messages.
func drain(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			drain(c)
		}
	default:
		_ = msg
	}
}
