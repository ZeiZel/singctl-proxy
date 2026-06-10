package ui

import (
	"context"
	"errors"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func loadLinkCmd(b Backend, link string) tea.Cmd {
	return func() tea.Msg {
		if err := b.LoadLink(context.Background(), link); err != nil {
			return errMsg{err}
		}
		return linkLoadedMsg{}
	}
}

func enableProxyCmd(b Backend) tea.Cmd {
	return func() tea.Msg {
		if err := b.EnableProxy(context.Background()); err != nil {
			return errMsg{err}
		}
		return proxyEnabledMsg{}
	}
}

func enableVPNCmd(b Backend) tea.Cmd {
	return func() tea.Msg {
		if err := b.EnableVPN(context.Background()); err != nil {
			return errMsg{err}
		}
		return vpnEnabledMsg{}
	}
}

func stopCmd(b Backend) tea.Cmd {
	return func() tea.Msg {
		if err := b.Stop(context.Background()); err != nil {
			return errMsg{err}
		}
		return stoppedMsg{}
	}
}

// readLogsCmd reads the sing-box log file (the UI never lets logs hit stdout).
func readLogsCmd(path string) tea.Cmd {
	return func() tea.Msg {
		if path == "" {
			return logsMsg{content: "(путь к логам не задан)"}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return logsMsg{content: "(логи пока недоступны — запустите режим)"}
		}
		return logsMsg{content: string(data)}
	}
}

// logsTick refreshes the logs view roughly once a second while it is open.
func logsTick() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return logsTickMsg{} })
}

// listen blocks for the next async message from the notes channel and reschedules
// itself. A closed channel yields an errMsg instead of hanging.
func listen(ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return errMsg{errors.New("status channel closed")}
		}
		return msg
	}
}
