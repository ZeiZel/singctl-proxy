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

	// dashboard navigation
	Next     key.Binding // Tab: move the раздел focus ring
	Prev     key.Binding // Shift+Tab
	Left     key.Binding
	Right    key.Binding
	Activate key.Binding
}

func defaultKeys(gl Glyphs) keyMap {
	return keyMap{
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
		Next:     key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "раздел")),
		Prev:     key.NewBinding(key.WithKeys("shift+tab")),
		Left:     key.NewBinding(key.WithKeys("left"), key.WithHelp(gl.ArrowsLR, "выбор")),
		Right:    key.NewBinding(key.WithKeys("right")),
		Activate: key.NewBinding(key.WithKeys("enter", " "), key.WithHelp(gl.Enter, "применить")),
	}
}

// ShortHelp is the single-line footer (truncated to width by help.Model).
func (k keyMap) ShortHelp() []key.Binding {
	// Kept short so it fits without truncation; Edit/Logs live in FullHelp ('?').
	return []key.Binding{k.Proxy, k.VPN, k.Stop, k.Conns, k.Proc, k.Help, k.Quit}
}

// FullHelp is the expanded, multi-column footer shown when '?' is pressed.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Proxy, k.VPN, k.Stop},
		{k.Conns, k.Logs, k.Proc, k.Edit, k.Settings},
		{k.Left, k.Activate, k.Next},
		{k.Help, k.Quit},
	}
}
