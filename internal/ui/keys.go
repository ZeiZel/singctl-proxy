package ui

import "github.com/charmbracelet/bubbles/key"

// keyMap is the declarative dashboard keymap. bubbles/help renders it into the
// footer (ShortHelp inline, FullHelp in columns) and truncates it to fit narrow
// terminals automatically. Single-key actions and the focus-driven selector
// share the same bindings, so the footer always documents the live controls.
type keyMap struct {
	Proxy    key.Binding
	VPN      key.Binding
	Stop     key.Binding
	Edit     key.Binding
	Logs     key.Binding
	Conns    key.Binding
	Proc     key.Binding
	Settings key.Binding
	Help     key.Binding
	Quit     key.Binding

	// dashboard / pane-grid navigation
	Next     key.Binding // Tab: move the focused pane forward
	Prev     key.Binding // Shift+Tab: move it back
	Left     key.Binding
	Right    key.Binding
	Activate key.Binding // Enter: expand the focused pane
	Expand   key.Binding // o: expand the focused pane
	Collapse key.Binding // esc: collapse back to the grid

	// darwin enables a macOS-only help block in FullHelp ('?'). macHint*
	// are documentation-only bindings (no live key) shown only on macOS.
	darwin      bool
	macHintProx key.Binding // Chromium/Electron --proxy-server recipe
	macHintDocs key.Binding // pointer to docs/macos.md
}

func defaultKeys(gl Glyphs, darwin bool) keyMap {
	return keyMap{
		darwin: darwin,
		macHintProx: key.NewBinding(key.WithKeys(""), key.WithHelp(
			"--proxy-server", "socks5://127.0.0.1:1080 (Chromium/Cursor)")),
		macHintDocs: key.NewBinding(key.WithKeys(""), key.WithHelp(
			"macOS", "перезапуск-в-прокси · docs/macos.md")),
		Proxy:    key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "прокси")),
		VPN:      key.NewBinding(key.WithKeys("v"), key.WithHelp("v", "vpn")),
		Stop:     key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "стоп")),
		Edit:     key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "ссылка")),
		Logs:     key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "логи")),
		Conns:    key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "соединения")),
		Proc:     key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "приложения")),
		Settings: key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "настройки")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "справка")),
		Quit:     key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "выход")),
		Next:     key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "панель")),
		Prev:     key.NewBinding(key.WithKeys("shift+tab")),
		Left:     key.NewBinding(key.WithKeys("left"), key.WithHelp(gl.ArrowsLR, "панель")),
		Right:    key.NewBinding(key.WithKeys("right")),
		Activate: key.NewBinding(key.WithKeys("enter", " "), key.WithHelp(gl.Enter, "открыть")),
		Expand:   key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "открыть")),
		Collapse: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "назад")),
	}
}

// ShortHelp is the single-line footer (truncated to width by help.Model).
func (k keyMap) ShortHelp() []key.Binding {
	// Kept short so it fits without truncation; Edit/Logs live in FullHelp ('?').
	return []key.Binding{k.Proxy, k.VPN, k.Stop, k.Conns, k.Proc, k.Help, k.Quit}
}

// FullHelp is the expanded, multi-column footer shown when '?' is pressed. On
// macOS it gains a documentation-only block (the Chromium --proxy-server recipe
// and a pointer to docs/macos.md), since per-PID interception is unavailable there.
func (k keyMap) FullHelp() [][]key.Binding {
	cols := [][]key.Binding{
		{k.Proxy, k.VPN, k.Stop},
		{k.Conns, k.Logs, k.Proc, k.Edit, k.Settings},
		{k.Left, k.Activate, k.Next},
		{k.Help, k.Quit},
	}
	if k.darwin {
		cols = append(cols, []key.Binding{k.macHintProx, k.macHintDocs})
	}
	return cols
}
