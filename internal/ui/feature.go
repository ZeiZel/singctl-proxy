package ui

import "singctl/internal/feature"

// FeatureDescriptor describes the terminal interface (no flags of its own).
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "ui",
		Title:   "Интерфейс",
		Summary: "дашборд, соединения, процессы, логи, ключи",
		Doc: "Терминальный интерфейс: дашборд с режимом (OFF/PROXY/VPN), панелью " +
			"статуса и живыми соединениями + задержками серверов; оверлеи соединений " +
			"(c), процессов (x) и логов (l); экран ключей подключения (e).",
	}
}
