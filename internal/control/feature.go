package control

import "singctl/internal/feature"

// FeatureDescriptor describes the instance attach/control feature for the CLI.
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "control",
		Title:   "Управление инстансом",
		Summary: "attach / stop / status",
		Doc: "Запущенный инстанс публикует instance.json и слушает Unix-сокет. " +
			"Эти команды работают без root: подключиться к логам уже запущенного " +
			"в другой вкладке инстанса, узнать статус или остановить его.",
		Flags: []feature.FlagSpec{
			{Names: []string{"attach"}, Usage: "подключиться к логам запущенного инстанса (ctrl+c — отсоединиться)"},
			{Names: []string{"stop"}, Usage: "остановить запущенный инстанс"},
			{Names: []string{"status"}, Usage: "показать статус запущенного инстанса"},
		},
	}
}
