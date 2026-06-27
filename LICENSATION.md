# LICENSATION — включение Apple-модулей для per-app изоляции (macOS)

Эта инструкция — последовательность действий, чтобы заработали Apple-модули,
которыми singctl изолирует трафик **отдельных приложений** на macOS: системное
расширение `NETransparentProxyProvider` (`com.singctl.proxy.netext`) + контейнер-
приложение, которое его активирует. Расширение перехватывает **весь** сетевой
стек выбранного приложения (Chromium, Node/undici, raw-сокеты), поэтому форки и
хелперы (например, extension-host Cursor) покрываются автоматически.

> **Важно: дистрибуция — Developer ID + нотаризация, НЕ App Store.**
> System Extension нельзя распространять через Mac App Store; VPN/TUN-режим
> требует root и несовместим с App-Sandbox (обязательным для Store); а голый CLI
> в Store не публикуется. Поэтому путь — подписанный Developer ID `.app`/`.pkg` +
> нотаризация, распространяемые вне Store. (Линукс-изоляция приложений работает
> без всего этого — бесплатно, через cgroup/nftables.)

## 0. Что понадобится

- **Apple Developer Program** — платное членство ($99/год). Бесплатный Apple ID
  **не подойдёт**: entitlement Network Extensions и нотаризация доступны только
  платным аккаунтам.
- **Xcode** (+ command line tools) и **XcodeGen** (`brew install xcodegen`).
- **Team ID** (10 символов). Где взять:
  - `security find-identity -v -p codesigning` → в скобках в конце строки;
  - или developer.apple.com/account → **Membership details** → Team ID;
  - или Xcode → Settings → Accounts → команда.

## 1. Идентификаторы (App IDs) и capabilities

В developer.apple.com/account → **Identifiers** создай два App ID (они должны
вкладываться: extension — суффикс к app):

| Роль | Bundle ID | Capabilities |
|---|---|---|
| Контейнер-приложение | `com.singctl.proxy` | App Groups, Network Extensions |
| System Extension | `com.singctl.proxy.netext` | App Groups, Network Extensions |

Создай **App Group** `group.com.singctl.proxy` и включи его у обоих App ID.
Этот App Group — общий контейнер, через который CLI отдаёт расширению список
приложений (`config.json`, см. `internal/netext`).

## 2. Provisioning profiles

Для каждого App ID создай профиль уровня **Developer ID** (для распространения вне
Store) с включённым `*-systemextension`-entitlement Network Extensions. На время
разработки можно начать с Development-профилей и автоподписи в Xcode.

## 3. Entitlements в репозитории

Файлы уже есть, при необходимости подставь свой Team ID / идентификаторы:
- `packaging/macos/netextension/ProxyExtension/ProxyExtension.entitlements`
- `packaging/macos/netextension/ContainerApp/ContainerApp.entitlements`

Оба объявляют `com.apple.developer.networking.networkextension`
(`*-systemextension`) и App Group `group.com.singctl.proxy`.

**CLI `singctl` тоже подпиши с App Group entitlement** — иначе он не сможет писать
общий `config.json` (`~/Library/Group Containers/group.com.singctl.proxy/
config.json`), и per-app изоляция вернёт ошибку записи в TUI. Минимальный
entitlements-файл для CLI:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>com.apple.security.application-groups</key>
  <array><string>group.com.singctl.proxy</string></array>
</dict></plist>
```

Подпись CLI:
```sh
codesign --force --options runtime \
  --entitlements singctl.entitlements \
  --sign "Developer ID Application: <Your Name> (<TEAM_ID>)" \
  bin/singctl
```

## 4. Сборка расширения + контейнера

```sh
make build-netext DEVELOPMENT_TEAM=<TEAM_ID>
# = packaging/macos/netextension/build.sh: xcodegen generate + xcodebuild Release
```

Альтернатива — вручную в Xcode: `cd packaging/macos/netextension && xcodegen
generate && open SingctlProxy.xcodeproj`, выставить Team у обоих таргетов,
Product → Archive.

## 5. Первый запуск и одобрение

1. Запусти собранный `SingctlProxy.app` один раз.
2. Одобри расширение: **System Settings → General → Login Items & Extensions →
   Network Extensions** (на старых macOS — Security & Privacy).
3. Разреши конфигурацию прокси, если система спросит.

Проверка статуса: `systemextensionsctl list` — у `com.singctl.proxy.netext`
должно быть состояние `[activated enabled]` (именно это проверяет
`netext.Available()`).

## 6. Нотаризация (обязательно для распространения)

```sh
# один раз сохранить креды notarytool:
xcrun notarytool store-credentials singctl-notary \
  --apple-id <you@example.com> --team-id <TEAM_ID> --password <app-specific-pwd>

# собрать .pkg/.zip с .app, затем:
xcrun notarytool submit SingctlProxy.zip --keychain-profile singctl-notary --wait
xcrun stapler staple SingctlProxy.app
```

## 7. Дистрибуция

Раздавай подписанный Developer ID + нотаризованный `.app`/`.pkg` вместе с
Developer-ID-подписанным CLI `singctl`. **Не** через App Store (см. врезку вверху).

## 8. Проверка, что изоляция работает

1. В singctl выбери приложение для изоляции (раздел «Приложения»). CLI запишет
   его bundle ID в `config.json`; расширение подхватит его (file-watch).
2. Убедись, что трафик приложения идёт через прокси:
   ```sh
   lsof -nP -p <PID процесса приложения> -i
   ```
   Соединения должны идти на `127.0.0.1:1080` (локальный SOCKS singctl), а не
   напрямую во внешний AWS/Cloudflare.

## Траблшутинг

- **`netext.Available()` = false / «расширение не одобрено»** — пройди шаг 5;
  проверь `systemextensionsctl list`.
- **Ошибка записи `config.json`** — CLI не подписан с App Group entitlement
  (шаг 3) или нет членства/контейнера.
- **`xcodebuild` падает на provisioning** — не включены capabilities Network
  Extensions/App Groups у App ID (шаг 1) или не выбран Team (шаг 4).
- **Расширение не грузится после установки** — нужна нотаризация (шаг 6) либо на
  dev-машине временно ослабить через `systemextensionsctl developer on`.
