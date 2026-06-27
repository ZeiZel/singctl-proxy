package license

import "singctl/internal/feature"

// FeatureDescriptor documents the license CLI surface for --help / the man page.
// internal/feature is pure, so importing it here introduces no cycle.
func FeatureDescriptor() feature.Descriptor {
	return feature.Descriptor{
		Name:    "license",
		Title:   "Лицензия",
		Summary: "активация и статус",
		Doc: "singctl требует действующую лицензию (офлайн-проверка по вшитому ключу). " +
			"Получите токен у поставщика, установите его `--license <токен|файл>`, " +
			"проверьте `--license-status`. Сборка `make build-unlicensed` отключает проверку.",
		Flags: []feature.FlagSpec{
			{Names: []string{"license"}, Placeholder: "<токен|путь>",
				Usage: "установить лицензию (токен или путь к файлу) и выйти",
				Env:   []string{"SINGCTL_LICENSE"}},
			{Names: []string{"license-status"},
				Usage: "показать статус лицензии и выйти"},
		},
	}
}
