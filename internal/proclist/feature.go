package proclist

import "singctl/internal/feature"

// FeatureDescriptor describes the process picker (a TUI-only feature, no flags).
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "processes",
		Title:   "Список процессов",
		Summary: "выбор процесса в TUI (клавиша x)",
		Doc: "Перечисляет процессы с сетевыми сокетами (PID, локальные порты, имя), " +
			"чтобы было проще найти нужный процесс для проксирования. macOS — lsof, " +
			"Linux — /proc.",
	}
}
