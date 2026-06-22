package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// safe wraps a command's body so a panic in the backend goroutine becomes a
// surfaced errMsg instead of crashing the whole program. The reducer records the
// errMsg into the action log, so a backend explosion degrades to a visible error.
// recover() is permitted here because this is a command goroutine, not Update/View.
func safe(fn func() tea.Msg) tea.Cmd {
	return func() (msg tea.Msg) {
		defer func() {
			if r := recover(); r != nil {
				msg = errMsg{fmt.Errorf("внутренняя ошибка: %v", r)}
			}
		}()
		return fn()
	}
}

// routePIDCmd routes an already-running PID through the proxy.
func routePIDCmd(b Backend, pid int) tea.Cmd {
	return safe(func() tea.Msg {
		if err := b.RoutePID(context.Background(), pid); err != nil {
			return procResultMsg{err: err}
		}
		return procResultMsg{note: fmt.Sprintf("PID %d проксируется", pid), pid: pid}
	})
}

// applySettingsCmd reloads the running core with edited settings.
func applySettingsCmd(b Backend, s Settings) tea.Cmd {
	return safe(func() tea.Msg {
		return settingsAppliedMsg{err: b.ApplySettings(context.Background(), s)}
	})
}

// daemonizeCmd re-execs a detached background process.
func daemonizeCmd(b Backend) tea.Cmd {
	return safe(func() tea.Msg {
		return daemonizedMsg{err: b.Daemonize(context.Background())}
	})
}

// stopDaemonCmd fully stops the attached background instance.
func stopDaemonCmd(b Backend) tea.Cmd {
	return safe(func() tea.Msg {
		return daemonStoppedMsg{err: b.StopDaemon(context.Background())}
	})
}

// frameCmd schedules the next animation frame (~25 fps).
func frameCmd() tea.Cmd {
	return tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return frameMsg{} })
}

// listProcessesCmd fetches the process list for the picker.
func listProcessesCmd(b Backend) tea.Cmd {
	return func() tea.Msg {
		rows, err := b.ListProcesses(context.Background())
		return procListMsg{rows: rows, err: err}
	}
}

// launchProcCmd starts a command with its traffic routed through the proxy.
func launchProcCmd(b Backend, argv []string) tea.Cmd {
	return safe(func() tea.Msg {
		pid, err := b.LaunchProxied(context.Background(), argv)
		if err != nil {
			return procResultMsg{err: err}
		}
		return procResultMsg{note: fmt.Sprintf("запущен PID %d через прокси", pid), pid: pid}
	})
}

// restartPIDCmd restarts a running PID in proxy mode.
func restartPIDCmd(b Backend, pid int) tea.Cmd {
	return safe(func() tea.Msg {
		newPID, err := b.RestartProxied(context.Background(), pid)
		if err != nil {
			return procResultMsg{err: err}
		}
		return procResultMsg{note: fmt.Sprintf("PID %d перезапущен в proxy-режиме (новый PID %d)", pid, newPID), pid: newPID}
	})
}

func loadLinkCmd(b Backend, link string) tea.Cmd {
	return func() tea.Msg {
		if err := b.LoadLink(context.Background(), link); err != nil {
			return errMsg{err}
		}
		return linkLoadedMsg{links: b.CurrentLinks()}
	}
}

// addLinkCmd appends another VLESS key (failover) and returns the refreshed set.
func addLinkCmd(b Backend, link string) tea.Cmd {
	return func() tea.Msg {
		if err := b.AddLink(context.Background(), link); err != nil {
			return errMsg{err}
		}
		return linkAddedMsg{links: b.CurrentLinks()}
	}
}

func enableProxyCmd(b Backend) tea.Cmd {
	return safe(func() tea.Msg {
		if err := b.EnableProxy(context.Background()); err != nil {
			return errMsg{err}
		}
		return proxyEnabledMsg{}
	})
}

func enableVPNCmd(b Backend) tea.Cmd {
	return safe(func() tea.Msg {
		if err := b.EnableVPN(context.Background()); err != nil {
			return errMsg{err}
		}
		return vpnEnabledMsg{}
	})
}

func stopCmd(b Backend) tea.Cmd {
	return safe(func() tea.Msg {
		if err := b.Stop(context.Background()); err != nil {
			return errMsg{err}
		}
		return stoppedMsg{}
	})
}

// readLogsCmd reads the sing-box log file (the UI never lets logs hit stdout).
func readLogsCmd(path string) tea.Cmd {
	return safe(func() tea.Msg {
		if path == "" {
			return logsMsg{content: "(путь к логам не задан)"}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return logsMsg{content: "(логи пока недоступны — запустите режим)"}
		}
		return logsMsg{content: string(data)}
	})
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
	// safe guards the body: a panic becomes an errMsg, but the reducer still
	// reschedules listen() on the next Update, so the async pump never dies.
	return safe(func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return errMsg{errors.New("status channel closed")}
		}
		return msg
	})
}
