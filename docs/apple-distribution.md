# Apple-дистрибуция singctl

Сквозной путь для macOS-сборки singctl: Developer ID, подпись, notarization и
раздача команды. Team ID проекта — `S3UCF4USYC`.

## Apple Developer

Создайте App IDs `com.singctl.proxy` и `com.singctl.proxy.netext`, включите
App Group `group.com.singctl.proxy`, Network Extensions и получите сертификаты
Developer ID Application и Developer ID Installer. Проверьте сертификаты:

```sh
security find-identity -v -p codesigning
```

Для notarization сохраните учётные данные через `xcrun notarytool
store-credentials`.

## Сборка

Обычный build содержит все функции; отдельные compile-time variants не требуются.

```sh
make build-macos
make app-macos DEVELOPMENT_TEAM=S3UCF4USYC
make pkg-macos PKG_VERSION=x.y.z CLI_BIN=bin/singctl-darwin-arm64
```

`app-macos` подписывает приложение и встроенное System Extension. `pkg-macos`
проверяет подпись приложения, подписывает CLI с App Group entitlement и при
наличии notarization credentials выполняет notarization/stapling.

## CI и установка

`.gitlab-ci.yml` запускает `release:macos` для тегов `vX.Y.Z`, а
`release:publish` загружает артефакты в Generic Package Registry и создаёт
GitLab Release. Go cache и стандартные `go vet`, `go test`, `go build` работают
в `test:go`.

Установите `.pkg` или `Singctl.app` из `.dmg`. При первом запуске одобрите
Network Extension в System Settings → General → Login Items & Extensions.
Подробности per-app режима — в [docs/macos.md](macos.md).
