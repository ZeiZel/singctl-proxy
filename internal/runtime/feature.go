package runtime

import "singctl/internal/feature"

// FeatureDescriptor describes the proxy/VPN mode + run options for the CLI.
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "proxy",
		Title:   "Режим и запуск",
		Summary: "proxy / VPN / порты",
		Doc: "Управление режимом работы: локальный proxy (SOCKS+HTTP) или " +
			"системный VPN (TUN). По умолчанию запускается TUI; --headless " +
			"запускает фоновый режим без интерфейса.",
		Flags: []feature.FlagSpec{
			{Names: []string{"p", "proxy"}, Usage: "включить proxy-режим при старте"},
			{Names: []string{"vpn"}, Usage: "включить VPN (TUN) режим при старте"},
			{Names: []string{"port"}, Placeholder: "<n>", Default: "1080/2080",
				Usage: "локальный SOCKS-порт (HTTP слушает на порт+1)", Env: []string{"SINGCTL_PORT"}},
			{Names: []string{"headless"}, Usage: "запуск без терминального интерфейса"},
			{Names: []string{"l", "logs"}, Usage: "стримить логи в stdout (headless) / открыть логи (TUI)"},
			{Names: []string{"daemon"}, Usage: "запустить отсоединённо в фоне и выйти (управление через --status/--stop)"},
		},
	}
}
