# singctl — сборка и запуск

TUI-утилита: поднимает VLESS-прокси на встроенном sing-box и переключает системный
VPN-режим (TUN), пассивно сосуществуя с Cisco Secure Client. Подробности — в `PLAN.md`.

## Требования

- **Go ≥ 1.23** (разработка велась на go1.26.3 darwin/arm64).
- **Xcode Command Line Tools** (`xcode-select --install`) — нужен `clang` для CGO
  (sing-box TUN на darwin собирается с `CGO_ENABLED=1`).
- Целевая платформа — **macOS arm64** (Apple Silicon). Решение D8.

## Команды

```
make test               # герметичный unit-набор: без root, без реального sing-box, без Cisco
make build              # сборка бинарника в ./bin/singctl (CGO_ENABLED=1)
make run                # сборка + запуск под sudo (VPN/TUN требует root)
make test-integration   # интеграционные тесты под root на живой машине (build-tag integration)
make tidy               # go mod tidy
make lint               # go vet
make install-man        # установка man-страницы (`man singctl`), может попросить sudo
```

## Кросс-платформенная сборка

```
make build-macos        # bin/singctl-darwin-{arm64,amd64}   (CGO_ENABLED=1, собирается на Mac)
make build-windows      # bin/singctl-windows-{amd64,arm64}.exe (Windows 10/11; чистый Go, CGO=0)
make build-all          # все четыре бинарника
```

Windows-сборка кросс-компилируется с любого хоста (CGO не нужен: sing-tun
использует wintun на Windows). macOS-сборка требует CGO и потому собирается на
Mac (обе архитектуры — clang кросс-ассемблирует).

Платформенные оговорки:

- **Windows 10/11:** запускать **от администратора**; для VPN (TUN) рядом с
  бинарником нужна `wintun.dll` (https://www.wintun.net). Проверка euid
  пропускается — права проверит сама ОС при создании TUN.
- Пассивная детекция Cisco Secure Client заточена под macOS (парсеры
  `ifconfig`/`netstat`/`ps`); на Windows она деградирует мягко (Cisco просто
  не обнаруживается), kernel-события маршрутов заменяет 2-секундный опрос.
  Очистка orphan-utun — тоже только macOS.

## Релиз macOS

Полный релиз собирается одной командой:

```sh
make release-macos RELEASE_VERSION=1.5.0
```

Это стамповка версии → `make build VERSION=` → `make app-macos` → `make
pkg-macos PKG_VERSION=` → проверка результата. На выходе:

```
dist/singctl-1.5.0.pkg
dist/singctl-1.5.0.dmg
```

### Предпосылки

- Xcode + `xcodegen` (`brew install xcodegen`) — нужны для сборки `Singctl.app`
  (пропустить сборку app можно через `--skip-app`, см. ниже).
- В login keychain должны быть сертификаты **Developer ID Application** и
  **Developer ID Installer**. `security find-identity -v -p codesigning` НЕ
  покажет Installer-сертификат (это не codesigning-сертификат) — проверять
  через `security find-identity -v` без фильтра. `release-macos.sh`
  автоопределяет обе идентичности сам; переопределить можно через
  `CODESIGN_IDENTITY` / `INSTALLER_IDENTITY`.

### Почему версия проставляется скриптом, а не пишется вручную в каждый артефакт

Версия хранится сразу в двух местах — `macos/Singctl/App/Info.plist` и
`macos/Singctl/ProxyExtension/Info.plist` (`CFBundleShortVersionString` /
`CFBundleVersion`) — и дополнительно линкуется в CLI из `VERSION` Makefile'а
(`-X main.version=...`). Без явного `VERSION=` Makefile берёт версию из `git
describe`, которая в неотмеченном или грязном дереве даёт что-то вроде
`782b7d7-dirty`. Версии приложения и System Extension ДОЛЖНЫ совпадать —
иначе macOS отказывается загружать расширение. Один релиз уже уходил под
номером 1.5.0 с бандлом приложения версии 1.4.1 внутри — именно поэтому
`release-macos.sh` в конце всё перепроверяет, а не доверяет тому, что
напечатали шаги сборки.

Проверка (`--- 5. Verify ---`) читает готовые артефакты с диска и сверяет:

- `bin/singctl --version` совпадает с релизной версией;
- версия `Singctl.app` (`CFBundleShortVersionString`) совпадает;
- версия `ProxyExtension.systemextension` совпадает;
- у `Singctl.app` есть иконка (`AppIcon.icns`) — эти ключи однажды терялись
  при правке `Info.plist` через PlistBuddy;
- готовый `.pkg` распаковывается, и версия проверяется у того, что реально
  поедет пользователю: у CLI и у бандла приложения ВНУТРИ пакета, а не у
  файлов, из которых он собирался (именно так и уехал 1.5.0 с приложением
  1.4.1);
- в этот же бинарник вкомпилирована поддержка XHTTP;
- теги сборки читаются обратно из бинарника (`go version -m`) и обязаны
  содержать `singbox`, `with_utls`, `with_clash_api`, `with_quic` — забытый тег
  не ломает сборку, он делает часть ключей молча нерабочими;
- `.pkg` и `.dmg` появились в `dist/`;
- если задан `INSTALLER_IDENTITY` — подпись `.pkg` валидна.

Любой провал — скрипт падает с «verification failed — do NOT ship these
artifacts».

### Нотаризация

Запускается только если задан `NOTARY_PROFILE` (keychain-профиль `xcrun
notarytool`). Без него артефакты подписаны, но не нотаризованы — Gatekeeper
будет ругаться при запуске на других машинах. Создать профиль один раз:

```sh
xcrun notarytool store-credentials <имя-профиля> \
  --apple-id <apple-id> --team-id <team-id> --password <app-specific-password>
```

### Установка сразу стартует демон

Постинсталл `.pkg` делает `launchctl bootstrap` и открывает приложение — то
есть установка сама по себе ЗАПУСКАЕТ демон. Поставить без автозапуска:

```sh
sudo installer -pkg dist/singctl-1.5.0.pkg -target /
sudo launchctl bootout system /Library/LaunchDaemons/com.singctl.proxy.plist
```

### Установка поверх старой версии — чистая замена, а не наслоение

`packaging/macos/scripts/preinstall` запускается ДО того, как payload лёг на
диск, и приводит машину в чистое состояние:

1. выгружает `com.singctl.proxy` (LaunchDaemon, `bootout`, затем legacy
   `unload -w` как запасной вариант);
2. выгружает пользовательский `com.singctl.pacserver` (LaunchAgent из `make
   pac-server`) — но только если на диске лежит именно singctl-овский агент
   (проверяется, что он реально раздаёт `~/.config/singctl` через
   `http.server`); свою собственную установку `~/projects/ExampleOrganization/mac-proxy` он не
   трогает вообще — это другой проект;
3. останавливает оставшиеся процессы `singctl`, но матчит их строго по
   реальному пути бинаря (`/usr/local/bin/singctl`), никогда голым
   `pkill singctl` — сначала `SIGTERM`, и только то, что не завершилось за
   пару секунд, добивает `SIGKILL`;
4. корректно завершает запущенный `Singctl.app` по bundle id;
5. удаляет предыдущую установку: `/usr/local/bin/singctl`,
   `/Applications/Singctl.app`, `/Library/LaunchDaemons/com.singctl.proxy.plist`.

После этого постинсталл кладёт новый `.plist`, грузит демон и **проверяет**,
что тот реально поднялся (недолгий опрос `launchctl print
system/com.singctl.proxy` и появления `~/.config/singctl/control.sock`), а не
просто верит успеху `bootstrap`. Если демон не поднялся за отведённое время —
в лог идёт явное предупреждение, но установка всё равно завершается успешно:
пакет установлен корректно, а запустить демон можно из GUI.

Что установщик **никогда** не трогает: `~/.config/singctl` (ключи, подписки,
сохранённый профиль) и `~/Library/Group Containers/group.com.singctl.proxy`
(общий App Group container) — это данные пользователя, а не часть
инсталляции.

`scripts/install-macos.sh` (CLI-путь `make install`) ведёт себя так же:
`stop_and_remove()` выгружает демон, останавливает процессы `singctl` тем же
TERM→KILL-путём по точному пути бинаря и удаляет старые `plist`/бинарь ПЕРЕД
установкой новых — та же функция, что уже используется в
`scripts/install-macos.sh uninstall`.

### Сборка без подписи установщика

`productsign` обязательно ходит за доверенной меткой времени на
`timestamp.apple.com`, а корпоративная сеть его блокирует: остальной интернет
работает, а этот хост не отвечает. Симптом — падение в самом конце, когда всё
уже собрано:

```
CMS signature encoding failed: The timestamp service is not available. (-67885)
productsign: error: Failed to sign the product.
```

Обойти можно только собрав `.pkg` без подписи (Gatekeeper будет ругаться на
чужих машинах, для локальной установки не мешает):

```sh
make release-macos RELEASE_VERSION=1.5.0 RELEASE_ARGS=--unsigned-installer
```

Флаги комбинируются: `RELEASE_ARGS="--skip-app --unsigned-installer"`.

### Пересобрать без `.app`

Если менялся только Go-код, самую долгую часть (сборку `Singctl.app`) можно
пропустить:

```sh
make release-macos RELEASE_VERSION=1.5.0 RELEASE_ARGS=--skip-app
```

## CLI-флаги

```
sudo singctl [flags]

  -k, --key <vless://...>  vless-ключ (ссылка) для загрузки
      --headless           без TUI: поднять режим и работать до ctrl+c
  -l, --logs               логи sing-box в stdout (headless) / открыть логи при старте (TUI)
  -p, --proxy              включить режим PROXY сразу при старте
      --vpn                включить режим VPN сразу при старте
      --port <n>           локальный SOCKS-порт (HTTP слушает на n+1; по умолчанию 1080/2080)
      --env-file <path>    загрузить переменные окружения из файла (по умолчанию ./.env)
      --no-save            не сохранять ключ в ~/.config/singctl
      --man                вывести man-страницу (roff) и выйти
  -v, --version            версия
  -h, --help               справка
```

Приоритет источников ключа/порта: **флаг → переменные окружения → сохранённый профиль**.
Переменные `SINGCTL_KEY` и `SINGCTL_PORT` читаются из окружения и из `.env`
(godotenv; уже установленные переменные `.env` не перекрывает).

Примеры:

```
sudo singctl --headless --proxy --port 7890 -k 'vless://...'
echo 'SINGCTL_KEY=vless://...' > .env && sudo singctl --headless --vpn --logs
```

## Важно

- Бинарник запускается **под sudo** целиком (решение §0/D2): создание TUN и `auto_route`
  требуют привилегий. Профили сохраняются в домашний каталог реального пользователя
  (резолв через `SUDO_USER`) и `chown`-ятся обратно.
- **Cisco не трогаем.** Состояние Cisco определяется только пассивным наблюдением за ОС;
  бинарники Cisco (`/opt/cisco/...`) не вызываются никогда — это проверяется тестом
  `internal/arch.TestNoCiscoBinaryReferences`.
- Версия sing-box — **v1.12.x или v1.13.x** (проверено на 1.13.12).
- **Feature-теги sing-box:** боевая сборка использует
  `-tags "singbox with_utls with_clash_api with_quic with_wireguard with_gvisor"`.
  Каждый тег закрывает конкретный протокол, и забытый тег не ломает сборку — он
  делает часть ключей молча нерабочими (конфиг разбирается, а построить узел
  ядро отказывается). Всё это ловит `make test-singbox-decode`.
  - `with_utls` — REALITY;
  - `with_quic` — hysteria, hysteria2, tuic;
  - `with_wireguard` + `with_gvisor` — WireGuard. gVisor нужен потому, что
    sing-box строит WireGuard-устройство на userspace-стеке; на наш собственный
    TUN это не влияет, он остаётся на `stack: "system"`.

  Раньше здесь было записано, что `with_gvisor` не собирается с запиненным
  `sing-tun`. Причина оказалась не в самом теге: в `go.mod` висела устаревшая
  версия `github.com/sagernet/gvisor`, а строка версии этого форка сортируется
  по semver как более новая, чем та, которую реально требуют `sing-box` и
  `sing-tun` (числовой идентификатор младше буквенно-цифрового). MVS молча
  выбирал несовместимую ревизию. После явного понижения до
  `v0.0.0-20250811.0-sing-box-mod.1` всё собирается. `with_gvisor` НЕ используем — берём
  `stack: system`, а сам он не собирается с запиненным `sing-tun`. Теги уже
  заданы в Makefile (`make build`).
