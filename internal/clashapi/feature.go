package clashapi

import "singctl/internal/feature"

// FeatureDescriptor describes the observability (Clash API) feature for the CLI.
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "observability",
		Title:   "Наблюдаемость",
		Summary: "Clash API + задержки серверов",
		Doc: "sing-box Clash API (только loopback, случайный секрет) опрашивается " +
			"для живых соединений (процесс-источник, назначение, цепочка) и задержек " +
			"серверов в группе failover.",
		Flags: []feature.FlagSpec{
			{Names: []string{"clash-api"}, Placeholder: "<host:port>", Default: "127.0.0.1:9090",
				Usage: "адрес Clash API (логи соединений + задержки)", Env: []string{"SINGCTL_CLASH_API"}},
			{Names: []string{"no-clash-api"}, Usage: "отключить Clash API"},
			{Names: []string{"clash-secret"}, Placeholder: "<s>", Default: "случайный за запуск",
				Usage: "секрет Clash API", Env: []string{"SINGCTL_CLASH_SECRET"}},
			{Names: []string{"urltest-url"}, Placeholder: "<url>", Default: "gstatic generate_204",
				Usage: "URL проверки серверов для failover"},
			{Names: []string{"urltest-interval"}, Placeholder: "<d>", Default: "3m",
				Usage: "интервал перепроверки серверов"},
			{Names: []string{"urltest-tolerance"}, Placeholder: "<ms>", Default: "50",
				Usage: "гистерезис переключения сервера, мс"},
		},
	}
}
