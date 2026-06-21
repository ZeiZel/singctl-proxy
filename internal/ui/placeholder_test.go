package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The edit screen ('e') must NOT echo the raw key (it is secret); it shows the
// add-key hint and the keys are rendered masked instead.
func TestEdit_DoesNotRevealKey(t *testing.T) {
	const link = "vless://uuid@host:443#srv"
	m := New(&fakeBackend{}, nil).WithLoadedProfile().
		WithCurrentLink(link).WithCurrentLinks([]string{link})

	m, _ = step(m, rune_("e"))
	if m.Screen() != ScreenLink {
		t.Fatal("'e' should open the link screen")
	}
	if m.input.Value() != "" {
		t.Error("input must start empty when editing")
	}
	if m.input.Placeholder == link {
		t.Errorf("placeholder must NOT reveal the raw key, got %q", m.input.Placeholder)
	}
	if m.input.Placeholder != "vless://… (добавить ключ)" {
		t.Errorf("placeholder = %q, want the add-key hint", m.input.Placeholder)
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

// After loading, the keys are remembered (for the masked connection-strings
// view) without ever echoing the raw key as a placeholder.
func TestLinkLoaded_RemembersKeysMasked(t *testing.T) {
	const link = "vless://new@host:443#srv"
	m := New(&fakeBackend{}, nil)
	m.input.SetValue(link)
	m, _ = step(m, linkLoadedMsg{links: []string{link}})
	m, _ = step(m, rune_("e"))
	if m.input.Placeholder == link {
		t.Errorf("placeholder must not reveal the key, got %q", m.input.Placeholder)
	}
	out := m.View()
	if strings.Contains(out, "new@host") {
		t.Errorf("connection-strings view leaked the key:\n%s", out)
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
