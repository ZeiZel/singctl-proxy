package vless

import "singctl/internal/feature"

// FeatureDescriptor describes the connection-keys feature for the CLI registry.
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "keys",
		Title:   "Ключи (VLESS)",
		Summary: "сервер(ы) подключения",
		Doc: "Один или несколько vless:// ключей. При нескольких ключах sing-box " +
			"собирает группу urltest и сам выбирает самый быстрый доступный сервер " +
			"(приоритет — порядок ввода).",
		Flags: []feature.FlagSpec{
			{Names: []string{"k", "key"}, Placeholder: "<vless://...>",
				Usage: "vless-ключ; повторяйте для нескольких серверов (failover)",
				Env:   []string{"SINGCTL_KEY", "SINGCTL_KEYS"}, Repeatable: true},
			{Names: []string{"no-save"}, Usage: "не сохранять ключ в ~/.config/singctl"},
		},
	}
}
