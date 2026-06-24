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
	if m.keyFocus != 0 {
		t.Fatalf("keys screen should open on the input, keyFocus=%d", m.keyFocus)
	}
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.keyFocus != 1 {
		t.Fatalf("Tab should focus the keys list, got %d", m.keyFocus)
	}
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.keyCursor != 1 {
		t.Errorf("Down should move the key cursor to 1, got %d", m.keyCursor)
	}
	m, _ = step(m, enterKey) // reveal
	if !m.keyReveal {
		t.Error("Enter on the list should reveal the focused key")
	}
}

func TestKeys_DeleteShowsConfirmThenDeletes(t *testing.T) {
	m, b := openKeys("vless://u@1.1.1.1:443#A", "vless://u@2.2.2.2:443#B")
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyTab}) // → list, cursor 0
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

func TestKeys_RenameFlow(t *testing.T) {
	m, b := openKeys("vless://u@1.1.1.1:443#Old")
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyTab}) // → list
	m, _ = step(m, rune_("n"))                   // rename → input, prefilled with "Old"
	if m.keyFocus != 0 || m.keyMode != keyModeRename {
		t.Fatalf("'n' should enter rename mode on the input (focus=%d mode=%d)", m.keyFocus, m.keyMode)
	}
	m.input.SetValue("NewName")
	m, cmd := step(m, enterKey)
	if cmd == nil {
		t.Fatal("rename should issue a command")
	}
	_ = cmd()
	if b.renamedIndex != 0 || b.renamedName != "NewName" {
		t.Errorf("RenameLink got (%d,%q), want (0,NewName)", b.renamedIndex, b.renamedName)
	}
	if m.keyMode != keyModeAdd {
		t.Error("after rename the input should return to add mode")
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
