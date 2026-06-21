package ui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

type fakeBackend struct {
	loadErr, proxyErr, vpnErr, stopErr         error
	loadCalls, proxyCalls, vpnCalls, stopCalls int
	lastLink                                   string

	routePIDErr  error
	launchErr    error
	routedPID    int
	launchedArgv []string
	launchPID    int
	procRows     []ProcInfo
	procListErr  error
	restartedPID int
	restartErr   error
	links        []string
	addedLink    string
	addErr       error
	applied      Settings
	applyErr     error
	applyCalls   int
	daemonCalls  int
	daemonErr    error
}

func (b *fakeBackend) LoadLink(_ context.Context, link string) error {
	b.loadCalls++
	b.lastLink = link
	if b.loadErr == nil {
		b.links = []string{link}
	}
	return b.loadErr
}
func (b *fakeBackend) AddLink(_ context.Context, link string) error {
	b.addedLink = link
	if b.addErr != nil {
		return b.addErr
	}
	b.links = append(b.links, link)
	return nil
}
func (b *fakeBackend) CurrentLinks() []string            { return b.links }
func (b *fakeBackend) EnableProxy(context.Context) error { b.proxyCalls++; return b.proxyErr }
func (b *fakeBackend) EnableVPN(context.Context) error   { b.vpnCalls++; return b.vpnErr }
func (b *fakeBackend) Stop(context.Context) error        { b.stopCalls++; return b.stopErr }
func (b *fakeBackend) RoutePID(_ context.Context, pid int) error {
	b.routedPID = pid
	return b.routePIDErr
}
func (b *fakeBackend) LaunchProxied(_ context.Context, argv []string) (int, error) {
	b.launchedArgv = argv
	if b.launchErr != nil {
		return 0, b.launchErr
	}
	if b.launchPID == 0 {
		b.launchPID = 4242
	}
	return b.launchPID, nil
}
func (b *fakeBackend) ListProcesses(context.Context) ([]ProcInfo, error) {
	return b.procRows, b.procListErr
}
func (b *fakeBackend) RestartProxied(_ context.Context, pid int) (int, error) {
	b.restartedPID = pid
	if b.restartErr != nil {
		return 0, b.restartErr
	}
	return pid + 1, nil
}
func (b *fakeBackend) ApplySettings(_ context.Context, s Settings) error {
	b.applyCalls++
	b.applied = s
	return b.applyErr
}
func (b *fakeBackend) Daemonize(context.Context) error {
	b.daemonCalls++
	return b.daemonErr
}

func rune_(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

var (
	enterKey = tea.KeyMsg{Type: tea.KeyEnter}
	escKey   = tea.KeyMsg{Type: tea.KeyEsc}
)

func step(m Model, msg tea.Msg) (Model, tea.Cmd) {
	upd, cmd := m.Update(msg)
	return upd.(Model), cmd
}

func TestLinkSubmit_Empty_ShowsError(t *testing.T) {
	m := New(&fakeBackend{}, nil)
	m, cmd := step(m, enterKey)
	if m.ErrText() == "" {
		t.Error("empty link should set an error")
	}
	if cmd != nil {
		t.Error("empty link should not issue a command")
	}
	if m.Screen() != ScreenLink {
		t.Error("should stay on link screen")
	}
}

func TestLinkSubmit_Valid_LoadsAndGoesToDashboardOff(t *testing.T) {
	b := &fakeBackend{}
	m := New(b, nil)
	m.input.SetValue("vless://uuid@host:443")
	m, cmd := step(m, enterKey)
	if cmd == nil {
		t.Fatal("expected a loadLink command")
	}
	msg := cmd() // runs backend.LoadLink
	if _, ok := msg.(linkLoadedMsg); !ok {
		t.Fatalf("expected linkLoadedMsg, got %T", msg)
	}
	m, _ = step(m, msg)
	if m.Screen() != ScreenDashboard {
		t.Errorf("screen = %v, want dashboard", m.Screen())
	}
	if m.Mode() != RunOff {
		t.Errorf("mode = %v, want OFF — nothing must auto-start", m.Mode())
	}
	if b.loadCalls != 1 || b.lastLink != "vless://uuid@host:443" {
		t.Errorf("LoadLink not called correctly: calls=%d link=%q", b.loadCalls, b.lastLink)
	}
	if b.proxyCalls != 0 || b.vpnCalls != 0 {
		t.Error("loading a link must NOT start any mode")
	}
}

func TestLinkSubmit_BackendError_StaysOnLink(t *testing.T) {
	b := &fakeBackend{loadErr: errors.New("invalid vless link")}
	m := New(b, nil)
	m.input.SetValue("garbage")
	m, cmd := step(m, enterKey)
	m, _ = step(m, cmd())
	if m.Screen() != ScreenLink {
		t.Error("should stay on link screen on error")
	}
	if m.ErrText() == "" {
		t.Error("error should be shown")
	}
}

func TestDashboard_EnableProxy(t *testing.T) {
	b := &fakeBackend{}
	m := New(b, nil).WithLoadedProfile()
	m, cmd := step(m, rune_("p"))
	if cmd == nil {
		t.Fatal("p should issue enable-proxy command")
	}
	m, _ = step(m, cmd())
	if m.Mode() != RunProxy {
		t.Errorf("mode = %v, want PROXY", m.Mode())
	}
	if b.proxyCalls != 1 {
		t.Errorf("EnableProxy calls = %d, want 1", b.proxyCalls)
	}
}

func TestDashboard_EnableVPN_Allowed(t *testing.T) {
	b := &fakeBackend{}
	m := New(b, nil).WithLoadedProfile()
	m.cisco = false
	m, cmd := step(m, rune_("v"))
	if cmd == nil {
		t.Fatal("expected enable-vpn command")
	}
	if m.ModalShown() {
		t.Error("no modal when Cisco inactive")
	}
	m, _ = step(m, cmd())
	if m.Mode() != RunVPN {
		t.Errorf("mode = %v, want VPN", m.Mode())
	}
	if b.vpnCalls != 1 {
		t.Errorf("EnableVPN calls = %d, want 1", b.vpnCalls)
	}
}

func TestDashboard_EnableVPN_CiscoActive_ShowsModalNoEnable(t *testing.T) {
	b := &fakeBackend{}
	m := New(b, nil).WithLoadedProfile()
	m.cisco = true
	m, cmd := step(m, rune_("v"))
	if !m.ModalShown() {
		t.Error("modal should be shown when Cisco active")
	}
	if cmd != nil {
		t.Error("must NOT issue enable command while Cisco active")
	}
	if b.vpnCalls != 0 {
		t.Error("EnableVPN must not be called")
	}
}

func TestDashboard_Stop(t *testing.T) {
	b := &fakeBackend{}
	m := New(b, nil).WithLoadedProfile()
	m.mode = RunVPN
	m, cmd := step(m, rune_("s"))
	if cmd == nil {
		t.Fatal("s should issue a stop command")
	}
	m, _ = step(m, cmd())
	if m.Mode() != RunOff {
		t.Errorf("mode = %v, want OFF after stop", m.Mode())
	}
	if b.stopCalls != 1 {
		t.Errorf("Stop calls = %d, want 1", b.stopCalls)
	}
}

func TestNetStateMsg_UpdatesCiscoIndicator(t *testing.T) {
	m := New(&fakeBackend{}, nil)
	m, _ = step(m, NetStateMsg{Cisco: true, PhysIface: "en0"})
	if !m.CiscoActive() {
		t.Error("netStateMsg should flip the Cisco indicator")
	}
}

func TestStatusMsg_ReflectsAutoSuspend(t *testing.T) {
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	m.mode = RunVPN
	// Executor auto-suspended (Cisco appeared) and notifies the UI.
	m, _ = step(m, StatusMsg{Mode: RunOff, Note: "Cisco активен — остановлено"})
	if m.Mode() != RunOff {
		t.Errorf("mode = %v, want OFF after auto-suspend", m.Mode())
	}
	if m.Status() == "" {
		t.Error("status note should be shown")
	}
}

func TestModalDismiss_OnAnyKey(t *testing.T) {
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	m.modal = "warning"
	m, _ = step(m, rune_("x"))
	if m.ModalShown() {
		t.Error("any key should dismiss the modal")
	}
}

func TestQuitKey(t *testing.T) {
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	_, cmd := step(m, rune_("q"))
	if cmd == nil {
		t.Fatal("q should issue a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("q should quit")
	}
}

func TestWindowResize(t *testing.T) {
	m := New(&fakeBackend{}, nil)
	m, _ = step(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	if m.width != 100 || m.height != 40 {
		t.Errorf("size = %dx%d, want 100x40", m.width, m.height)
	}
}

func TestChannelClosed_EmitsErrorNotHang(t *testing.T) {
	ch := make(chan tea.Msg)
	close(ch)
	cmd := listen(ch)
	if cmd == nil {
		t.Fatal("listen should return a command")
	}
	if _, ok := cmd().(errMsg); !ok {
		t.Fatalf("closed channel should yield errMsg, got %T", cmd())
	}
}

func TestView_DoesNotPanic(t *testing.T) {
	m := New(&fakeBackend{}, nil)
	m, _ = step(m, tea.WindowSizeMsg{Width: 90, Height: 30})
	_ = m.View() // link
	m = m.WithLoadedProfile()
	_ = m.View() // dashboard OFF
	m.mode = RunProxy
	_ = m.View()
	m.mode = RunVPN
	_ = m.View()
	m.cisco = true
	_ = m.View()
	m.modal = "x"
	_ = m.View()
	m.modal = ""
	m.showLogs = true
	_ = m.View()
}
