package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestMaskLink_HidesSecretKeepsName(t *testing.T) {
	link := "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4:443?security=tls#MyServer"
	got := maskLink(link, "•")
	if strings.Contains(got, "4ce58870") || strings.Contains(got, "1.2.3.4") {
		t.Errorf("masked link leaks secret: %q", got)
	}
	if !strings.Contains(got, "MyServer") {
		t.Errorf("masked link should keep the #name label: %q", got)
	}
	if !strings.Contains(got, "•") {
		t.Errorf("masked link should contain bullets: %q", got)
	}
}

func TestMaskLink_NoName(t *testing.T) {
	got := maskLink("vless://uuid@host:443", "*")
	if strings.Contains(got, "host") || strings.Contains(got, "uuid") {
		t.Errorf("masked link leaks: %q", got)
	}
}

func TestLinkView_ShowsMaskedKeysAndAddField(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps())
	m = m.WithCurrentLinks([]string{"vless://uuid@1.2.3.4:443#First"})
	m.screen = ScreenLink
	m.width, m.height = 100, 30
	m.relayout()
	out := m.View()
	if strings.Contains(out, "1.2.3.4") || strings.Contains(out, "uuid") {
		t.Errorf("connection-strings view must not reveal the key:\n%s", out)
	}
	if !strings.Contains(out, "First") {
		t.Errorf("should show the key's name label:\n%s", out)
	}
	if !strings.Contains(out, "Ключ 1") {
		t.Errorf("loaded key should be labelled 'Ключ 1':\n%s", out)
	}
	if !strings.Contains(out, "Ключ 2 — добавить ключ") {
		t.Errorf("add field should be labelled 'Ключ 2 — добавить ключ':\n%s", out)
	}
}

func TestLinkScreen_EnterAddsWhenKeysLoaded(t *testing.T) {
	b := &fakeBackend{links: []string{"vless://uuid@1.1.1.1:443#A"}}
	m := newWithCaps(b, nil, asciiCaps())
	m = m.WithCurrentLinks(b.links)
	m.screen = ScreenLink
	m.input.SetValue("vless://uuid@2.2.2.2:443#B")

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = next
	if cmd == nil {
		t.Fatal("expected an add command")
	}
	msg := cmd()
	if _, ok := msg.(linkAddedMsg); !ok {
		t.Fatalf("expected linkAddedMsg, got %T", msg)
	}
	if b.addedLink != "vless://uuid@2.2.2.2:443#B" {
		t.Errorf("AddLink got %q", b.addedLink)
	}
	// Applying the message updates the masked list to 2 keys.
	m2, _ := m.Update(msg)
	if len(m2.(Model).currentLinks) != 2 {
		t.Errorf("after add, currentLinks = %v, want 2", m2.(Model).currentLinks)
	}
}

func TestLinkScreen_EnterLoadsFirstWhenEmpty(t *testing.T) {
	b := &fakeBackend{}
	m := newWithCaps(b, nil, asciiCaps())
	m.screen = ScreenLink
	m.input.SetValue("vless://uuid@1.1.1.1:443#A")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a load command")
	}
	if msg := cmd(); func() bool { _, ok := msg.(linkLoadedMsg); return !ok }() {
		t.Fatalf("expected linkLoadedMsg for first key")
	}
	if b.loadCalls != 1 {
		t.Errorf("LoadLink calls = %d, want 1", b.loadCalls)
	}
}
