package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// openKeys returns a loaded model on the Ключи screen with the given links.
func openKeys(links ...string) (Model, *fakeBackend) {
	b := &fakeBackend{links: links}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile().WithCurrentLinks(links)
	m, _ = step(m, tea.WindowSizeMsg{Width: 90, Height: 30})
	m, _ = step(m, rune_("e")) // dashboard 'e' opens Ключи (ScreenLink)
	return m, b
}

func TestKeys_TabToListAndArrowMove(t *testing.T) {
	m, _ := openKeys("vless://u@1.1.1.1:443#A", "vless://u@2.2.2.2:443#B")
	// With keys loaded, the screen opens on the LIST so actions work immediately.
	if m.keyFocus != 1 {
		t.Fatalf("keys screen should open on the list, keyFocus=%d", m.keyFocus)
	}
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.keyCursor != 1 {
		t.Errorf("Down should move the key cursor to 1, got %d", m.keyCursor)
	}
	m, _ = step(m, enterKey) // reveal
	if !m.keyReveal {
		t.Error("Enter on the list should reveal the focused key")
	}
	// Tab toggles to the add-input.
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.keyFocus != 0 {
		t.Fatalf("Tab should focus the add-input, got %d", m.keyFocus)
	}
}

func TestKeys_DeleteShowsConfirmThenDeletes(t *testing.T) {
	m, b := openKeys("vless://u@1.1.1.1:443#A", "vless://u@2.2.2.2:443#B")
	// Opens on the list (cursor 0); 'd' confirms deletion.
	m, _ = step(m, rune_("d"))
	if !m.ModalShown() || m.modalKind != modalConfirm {
		t.Fatalf("'d' should open a confirm modal (shown=%v kind=%d)", m.ModalShown(), m.modalKind)
	}
	// Cancel with 'n' dismisses the modal and keeps both keys.
	mc, _ := step(m, rune_("n"))
	if mc.ModalShown() {
		t.Error("'n' should dismiss the confirm modal")
	}
	if len(mc.currentLinks) != 2 {
		t.Errorf("cancel should not delete; links=%d", len(mc.currentLinks))
	}
	// Confirm with 'y' deletes index 0.
	m, cmd := step(m, rune_("y"))
	if cmd == nil {
		t.Fatal("confirm should issue a delete command")
	}
	msg := cmd()
	if b.deletedIndex != 0 {
		t.Errorf("DeleteLink index = %d, want 0", b.deletedIndex)
	}
	m, _ = step(m, msg)
	if len(m.currentLinks) != 1 {
		t.Errorf("after delete, currentLinks = %d, want 1", len(m.currentLinks))
	}
}

func TestKeys_RenameOpensPopupThenRenames(t *testing.T) {
	m, b := openKeys("vless://u@1.1.1.1:443#Old")
	// Opens on the list; 'n' opens the rename input popup, prefilled with "Old".
	m, _ = step(m, rune_("n"))
	if !m.ModalShown() || m.modalKind != modalInput {
		t.Fatalf("'n' should open an input popup (shown=%v kind=%d)", m.ModalShown(), m.modalKind)
	}
	if got := m.prompt.Value(); got != "Old" {
		t.Errorf("rename popup should be prefilled with the current name, got %q", got)
	}
	m.prompt.SetValue("NewName")
	m, cmd := step(m, enterKey)
	if cmd == nil {
		t.Fatal("rename should issue a command")
	}
	_ = cmd()
	if b.renamedIndex != 0 || b.renamedName != "NewName" {
		t.Errorf("RenameLink got (%d,%q), want (0,NewName)", b.renamedIndex, b.renamedName)
	}
	if m.ModalShown() {
		t.Error("the popup should close after Enter")
	}
}

func TestKeys_RenamePopupCancels(t *testing.T) {
	m, b := openKeys("vless://u@1.1.1.1:443#Old")
	m, _ = step(m, rune_("n"))
	m.prompt.SetValue("X")
	m, _ = step(m, escKey)
	if m.ModalShown() {
		t.Error("Esc should close the rename popup")
	}
	if b.renamedName != "" {
		t.Errorf("Esc must not rename, got %q", b.renamedName)
	}
}

func TestProxied_UnrouteRemovesFromList(t *testing.T) {
	b := &fakeBackend{}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 90, Height: 30})
	m, _ = step(m, rune_("x")) // open Приложения
	m.proxied = []proxiedApp{{PID: 42, Name: "zen"}}
	m.appFocus = 2
	m, cmd := step(m, rune_("u"))
	if cmd == nil {
		t.Fatal("'u' should issue an unroute command")
	}
	msg := cmd()
	if b.unroutedPID != 42 {
		t.Errorf("UnroutePID got %d, want 42", b.unroutedPID)
	}
	m, _ = step(m, msg) // procResultMsg{remove:true}
	if len(m.proxied) != 0 {
		t.Errorf("unrouted app should leave the proxied list, got %d", len(m.proxied))
	}
}

func TestProxied_KillConfirmsThenTerminates(t *testing.T) {
	b := &fakeBackend{}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 90, Height: 30})
	m, _ = step(m, rune_("x"))
	m.proxied = []proxiedApp{{PID: 77, Name: "cursor"}}
	m.appFocus = 2
	m, _ = step(m, rune_("k"))
	if !m.ModalShown() || m.modalKind != modalConfirm {
		t.Fatal("'k' should open a kill-confirm modal")
	}
	m, cmd := step(m, rune_("y"))
	if cmd == nil {
		t.Fatal("confirm should issue a stop command")
	}
	msg := cmd()
	if b.stoppedPID != 77 {
		t.Errorf("StopProxied got %d, want 77", b.stoppedPID)
	}
	m, _ = step(m, msg)
	if len(m.proxied) != 0 {
		t.Errorf("killed app should leave the proxied list, got %d", len(m.proxied))
	}
}

func TestProxied_ResultAddsNamedApp(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, procResultMsg{note: "ok", pid: 1234, app: "zen"})
	if len(m.proxied) != 1 || m.proxied[0].PID != 1234 || m.proxied[0].Name != "zen" {
		t.Fatalf("procResultMsg should add a named proxied app, got %+v", m.proxied)
	}
	// A duplicate PID updates rather than appends.
	m, _ = step(m, procResultMsg{note: "ok", pid: 1234, app: "zen2"})
	if len(m.proxied) != 1 {
		t.Errorf("same PID should not duplicate, got %d", len(m.proxied))
	}
}
