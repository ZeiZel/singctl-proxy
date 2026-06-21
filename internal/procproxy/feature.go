package procproxy

import "singctl/internal/feature"

// FeatureDescriptor describes per-process proxying for the CLI.
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "procproxy",
		Title:   "Проксирование процессов",
		Summary: "по PID / запуск / перезапуск",
		Doc: "На Linux — настоящий перехват трафика процесса по PID (cgroup v2 + " +
			"nftables). На остальных ОС — запуск команды с проброшенными proxy-env. " +
			"--restart-pid перезапускает уже запущенный процесс в proxy-режиме.",
		Flags: []feature.FlagSpec{
			{Names: []string{"route-pid"}, Placeholder: "<pid>", Repeatable: true,
				Usage: "маршрутизировать процесс по PID через прокси (Linux)"},
			{Names: []string{"restart-pid"}, Placeholder: "<pid>", Repeatable: true,
				Usage: "перезапустить процесс в proxy-режиме"},
			{Names: []string{"launch"}, Placeholder: "-- <cmd>",
				Usage: "запустить команду через прокси"},
		},
	}
}
